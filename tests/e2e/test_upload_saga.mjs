import {
  signUpTestUser, signInTestUser, waitForNotification, waitForRow,
  getCreditBalance, cleanupUser, runTest, API_GATEWAY_URL
} from './helpers.mjs'

const passed = await runTest('Upload Saga: upload-intent → async notification → MinIO PUT → credit deduction', async () => {
  // 1. Sign up a fresh user (triggers Flink +2 credit via signup event)
  const { user, email, password } = await signUpTestUser()
  console.log(`  Created test user: ${email} (${user.id})`)

  try {
    // 2. Wait for credit initialization (Flink processes signup → credit_ledger)
    await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.signup_bonus' }, { timeoutMs: 45000 })
    const initialBalance = await getCreditBalance(user.id)
    console.log(`  Initial credit balance: ${initialBalance}`)
    if (initialBalance !== 2) throw new Error(`Expected initial balance=2, got ${initialBalance}`)

    // 3. Sign in to get a fresh session token
    const session = await signInTestUser(email, password)
    const token = session.access_token
    console.log(`  Signed in, got access token`)

    // 4. POST /api/v1/media/upload-intent — async saga endpoint, returns 202 + request_id
    const intentReq = {
      file_name: 'e2e-saga-upload-test.png',
      media_type: 'image/png',
      file_size: 1024
    }

    const intentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/upload-intent`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify(intentReq)
    })

    if (intentRes.status === 404 || intentRes.status === 501) {
      throw new Error(`upload-intent endpoint not available (status ${intentRes.status}). Is the intent handler wired in main.go?`)
    }
    if (intentRes.status !== 202) {
      const errBody = await intentRes.text()
      throw new Error(`Expected 202 from upload-intent, got ${intentRes.status}: ${errBody}`)
    }

    const intentBody = await intentRes.json()
    const { request_id: requestId } = intentBody
    console.log(`  Got 202 Accepted, request_id=${requestId}`)

    if (!requestId) throw new Error('Missing request_id in upload-intent response')

    // 5. Wait for media.upload_ready notification (Flink credit check → approved → presigned URL generated)
    console.log(`  Waiting for media.upload_ready notification...`)
    const uploadReadyNotif = await waitForNotification(user.id, 'media.upload_ready', { timeoutMs: 60000 })
    const uploadReadyPayload = JSON.parse(uploadReadyNotif.payload)
    console.log(`  Received media.upload_ready: request_id=${uploadReadyPayload.request_id}, file_path=${uploadReadyPayload.file_path}`)

    if (uploadReadyPayload.request_id !== requestId) {
      throw new Error(`request_id mismatch: expected ${requestId}, got ${uploadReadyPayload.request_id}`)
    }
    if (!uploadReadyPayload.upload_url) throw new Error('Missing upload_url in media.upload_ready payload')
    if (!uploadReadyPayload.file_path) throw new Error('Missing file_path in media.upload_ready payload')
    if (!uploadReadyPayload.expires_in) throw new Error('Missing expires_in in media.upload_ready payload')

    const { upload_url: uploadUrl, file_path: filePath } = uploadReadyPayload

    if (!filePath.startsWith('uploads/')) throw new Error(`Unexpected file_path format: ${filePath}`)
    if (!filePath.includes(user.id)) throw new Error(`file_path should contain user_id: ${filePath}`)

    // 6. PUT test file directly to MinIO using the presigned URL.
    //    The gateway signs URLs against host.docker.internal:9000 (K8s internal).
    //    Node's fetch (undici) strips custom Host headers, so we use http.request
    //    to connect to localhost:9000 while sending Host: host.docker.internal:9000
    //    to satisfy the S3v4 signature verification.
    const testFileContent = Buffer.from('PNG_E2E_SAGA_UPLOAD_TEST_' + Date.now())
    const parsedUrl = new URL(uploadUrl)
    const actualHost = parsedUrl.hostname   // host.docker.internal
    const actualPort = parsedUrl.port || 9000
    const http = await import('http')

    const putRes = await new Promise((resolve, reject) => {
      const req = http.default.request({
        method: 'PUT',
        hostname: '127.0.0.1',
        port: actualPort,
        path: parsedUrl.pathname + parsedUrl.search,
        headers: {
          'Content-Type': 'image/png',
          'Content-Length': testFileContent.length,
          'Host': `${actualHost}:${actualPort}`
        }
      }, (res) => {
        let body = ''
        res.on('data', (chunk) => body += chunk)
        res.on('end', () => resolve({ ok: res.statusCode >= 200 && res.statusCode < 300, status: res.statusCode, text: () => body }))
      })
      req.on('error', reject)
      req.write(testFileContent)
      req.end()
    })

    if (!putRes.ok) {
      const errBody = putRes.text()
      throw new Error(`PUT to presigned URL failed (${putRes.status}): ${errBody}`)
    }
    console.log(`  File uploaded to MinIO (${testFileContent.length} bytes)`)

    // 7. Wait for Flink to process the MinIO webhook event → media_files row
    //    MinIO webhook → Kafka → Flink SQL → media_files (status='active')
    console.log(`  Waiting for Flink to create media_files row...`)
    const mediaRow = await waitForRow('media_files', { user_id: user.id, file_name: 'e2e-saga-upload-test.png' }, { timeoutMs: 60000 })
    console.log(`  media_files entry: file_name=${mediaRow.file_name}, status=${mediaRow.status}, media_type=${mediaRow.media_type}`)

    if (mediaRow.status !== 'active') throw new Error(`Expected status='active', got ${mediaRow.status}`)
    if (mediaRow.media_type !== 'image/png') throw new Error(`Expected media_type='image/png', got ${mediaRow.media_type}`)

    // 8. Wait for credit deduction in credit_ledger
    console.log(`  Waiting for credit deduction in credit_ledger...`)
    const uploadLedger = await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.upload_deducted' }, { timeoutMs: 30000 })
    console.log(`  credit_ledger deduction: amount=${uploadLedger.amount}`)
    if (uploadLedger.amount !== -1) throw new Error(`Expected amount=-1, got ${uploadLedger.amount}`)

    // 9. Verify final balance=1
    const finalBalance = await getCreditBalance(user.id)
    console.log(`  Final credit balance: ${finalBalance}`)
    if (finalBalance !== 1) throw new Error(`Expected final balance=1, got ${finalBalance}`)

    // 10. Wait for media.upload_completed notification
    console.log(`  Waiting for media.upload_completed notification...`)
    const uploadCompletedNotif = await waitForNotification(user.id, 'media.upload_completed', { timeoutMs: 30000 })
    const uploadCompletedPayload = JSON.parse(uploadCompletedNotif.payload)
    console.log(`  Received media.upload_completed: file_path=${uploadCompletedPayload.file_path}, file_name=${uploadCompletedPayload.file_name}`)

    if (!uploadCompletedPayload.file_path) throw new Error('Missing file_path in media.upload_completed payload')
    if (!uploadCompletedPayload.file_name) throw new Error('Missing file_name in media.upload_completed payload')
    if (uploadCompletedPayload.file_name !== 'e2e-saga-upload-test.png') {
      throw new Error(`Unexpected file_name in upload_completed: ${uploadCompletedPayload.file_name}`)
    }

  } finally {
    await cleanupUser(user.id)
  }
})

// ---------------------------------------------------------------------------
// Test 2: after upload_completed notification, file is queryable in media_files
// This catches the race condition where refreshMediaBrowser() ran immediately
// after PUT to MinIO before Flink had time to INSERT into media_files.
// ---------------------------------------------------------------------------
const passed2 = await runTest('Upload saga: file appears in media_files after upload_completed notification', async () => {
  // 1. Sign up a fresh user
  const { user, email, password } = await signUpTestUser()
  console.log(`  Created test user: ${email} (${user.id})`)

  try {
    // 2. Wait for credit initialization
    await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.signup_bonus' }, { timeoutMs: 45000 })

    // 3. Sign in
    const session = await signInTestUser(email, password)
    const token = session.access_token
    console.log(`  Signed in, got access token`)

    // 4. POST upload-intent
    const intentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/upload-intent`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify({ file_name: 'e2e-saga-roundtrip-test.png', media_type: 'image/png', file_size: 1024 })
    })

    if (intentRes.status !== 202) {
      const errBody = await intentRes.text()
      throw new Error(`Expected 202 from upload-intent, got ${intentRes.status}: ${errBody}`)
    }

    const { request_id: requestId } = await intentRes.json()
    if (!requestId) throw new Error('Missing request_id in upload-intent response')
    console.log(`  Got request_id=${requestId}`)

    // 5. Wait for upload_ready notification — parse payload to get presigned URL
    const uploadReadyNotif = await waitForNotification(user.id, 'media.upload_ready', { timeoutMs: 60000 })
    const uploadReadyPayload = JSON.parse(uploadReadyNotif.payload)
    if (uploadReadyPayload.request_id !== requestId) {
      throw new Error(`request_id mismatch in upload_ready: expected ${requestId}, got ${uploadReadyPayload.request_id}`)
    }
    const { upload_url: uploadUrl, file_path: filePath } = uploadReadyPayload
    console.log(`  upload_ready received, file_path=${filePath}`)

    // 6. PUT file to MinIO
    const testFileContent = Buffer.from('PNG_E2E_ROUNDTRIP_TEST_' + Date.now())
    const parsedUrl = new URL(uploadUrl)
    const actualHost = parsedUrl.hostname
    const actualPort = parsedUrl.port || 9000
    const http = await import('http')

    const putRes = await new Promise((resolve, reject) => {
      const req = http.default.request({
        method: 'PUT',
        hostname: '127.0.0.1',
        port: actualPort,
        path: parsedUrl.pathname + parsedUrl.search,
        headers: {
          'Content-Type': 'image/png',
          'Content-Length': testFileContent.length,
          'Host': `${actualHost}:${actualPort}`
        }
      }, (res) => {
        let body = ''
        res.on('data', (chunk) => body += chunk)
        res.on('end', () => resolve({ ok: res.statusCode >= 200 && res.statusCode < 300, status: res.statusCode }))
      })
      req.on('error', reject)
      req.write(testFileContent)
      req.end()
    })

    if (!putRes.ok) throw new Error(`PUT to presigned URL failed (${putRes.status})`)
    console.log(`  File PUT to MinIO`)

    // 7. Wait for media.upload_completed notification (matched by file_path, no request_id in payload)
    //    This is the event the frontend now waits for before calling refreshMediaBrowser().
    //    Only after this notification has arrived can we trust that media_files has been INSERTed.
    console.log(`  Waiting for media.upload_completed notification (file_path=${filePath})...`)
    const uploadCompletedNotif = await waitForNotification(user.id, 'media.upload_completed', { timeoutMs: 60000 })
    const uploadCompletedPayload = JSON.parse(uploadCompletedNotif.payload)
    console.log(`  Received media.upload_completed: file_path=${uploadCompletedPayload.file_path}`)

    const expectedPermanentPath = filePath.replace(/^uploads\//, 'files/')
    if (uploadCompletedPayload.file_path !== expectedPermanentPath) {
      throw new Error(`file_path mismatch in upload_completed: expected ${expectedPermanentPath}, got ${uploadCompletedPayload.file_path}`)
    }
    if (!uploadCompletedPayload.file_name) throw new Error('Missing file_name in upload_completed payload')

    // 8. NOW query media_files — the row must exist with status='active'.
    //    This asserts that when the frontend would call refreshMediaBrowser() (triggered by
    //    upload_completed), the Flink INSERT has already committed and the file is visible.
    console.log(`  Querying media_files for permanent path=${expectedPermanentPath}...`)
    const mediaRow = await waitForRow('media_files', { user_id: user.id, file_name: 'e2e-saga-roundtrip-test.png' }, { timeoutMs: 5000 })
    console.log(`  media_files row found: status=${mediaRow.status}, media_type=${mediaRow.media_type}`)

    if (mediaRow.status !== 'active') {
      throw new Error(`Expected status='active' in media_files after upload_completed, got ${mediaRow.status}`)
    }
    if (mediaRow.file_path !== expectedPermanentPath) {
      throw new Error(`file_path mismatch in media_files row: expected ${expectedPermanentPath}, got ${mediaRow.file_path}`)
    }

    console.log(`  Full round-trip verified: upload_completed notification preceded queryable media_files row`)

  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed && passed2 ? 0 : 1)
