import {
  signUpTestUser, signInTestUser, waitForNotification, waitForRow,
  getCreditBalance, cleanupUser, runTest, API_GATEWAY_URL
} from './helpers.mjs'

const passed = await runTest('Move Saga: upload lands in files/ prefix with credit deduction', async () => {
  // 1. Sign up a fresh user
  const { user, email, password } = await signUpTestUser()
  console.log(`  Created test user: ${email} (${user.id})`)

  try {
    // 2. Wait for credit initialization
    await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.signup_bonus' }, { timeoutMs: 45000 })
    const initialBalance = await getCreditBalance(user.id)
    console.log(`  Initial credit balance: ${initialBalance}`)
    if (initialBalance !== 2) throw new Error(`Expected initial balance=2, got ${initialBalance}`)

    // 3. Sign in
    const session = await signInTestUser(email, password)
    const token = session.access_token

    // 4. POST upload-intent
    const intentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/upload-intent`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify({
        file_name: 'e2e-move-saga-test.png',
        media_type: 'image/png',
        file_size: 2048
      })
    })

    if (intentRes.status !== 202) {
      const errBody = await intentRes.text()
      throw new Error(`Expected 202, got ${intentRes.status}: ${errBody}`)
    }

    const { request_id: requestId } = await intentRes.json()
    console.log(`  Got 202 Accepted, request_id=${requestId}`)

    // 5. Wait for upload_ready notification
    const uploadReadyNotif = await waitForNotification(user.id, 'media.upload_ready', { timeoutMs: 60000 })
    const uploadReadyPayload = JSON.parse(uploadReadyNotif.payload)
    const { upload_url: uploadUrl, file_path: filePath } = uploadReadyPayload
    console.log(`  upload_ready: file_path=${filePath}`)

    if (!filePath.startsWith('uploads/')) throw new Error(`Expected uploads/ prefix, got ${filePath}`)

    // 6. PUT file to MinIO
    const testFileContent = Buffer.from('PNG_E2E_MOVE_SAGA_TEST_' + Date.now())
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

    if (!putRes.ok) throw new Error(`PUT failed (${putRes.status})`)
    console.log(`  File uploaded to MinIO (${testFileContent.length} bytes)`)

    // 7. Wait for media.upload_completed notification
    console.log(`  Waiting for media.upload_completed...`)
    const completedNotif = await waitForNotification(user.id, 'media.upload_completed', { timeoutMs: 90000 })
    const completedPayload = JSON.parse(completedNotif.payload)
    console.log(`  upload_completed: file_path=${completedPayload.file_path}`)

    // 8. Verify file_path starts with files/ (permanent prefix)
    if (!completedPayload.file_path.startsWith('files/')) {
      throw new Error(`Expected files/ prefix in upload_completed, got ${completedPayload.file_path}`)
    }

    // 9. Verify the permanent path is the uploads/ path with prefix swapped
    const expectedPermanentPath = filePath.replace(/^uploads\//, 'files/')
    if (completedPayload.file_path !== expectedPermanentPath) {
      throw new Error(`file_path mismatch: expected ${expectedPermanentPath}, got ${completedPayload.file_path}`)
    }

    // 10. Verify media_files row has permanent path
    const mediaRow = await waitForRow('media_files', { user_id: user.id, file_name: 'e2e-move-saga-test.png' }, { timeoutMs: 30000 })
    console.log(`  media_files: file_path=${mediaRow.file_path}, status=${mediaRow.status}`)

    if (mediaRow.status !== 'active') throw new Error(`Expected status=active, got ${mediaRow.status}`)
    if (!mediaRow.file_path.startsWith('files/')) {
      throw new Error(`Expected files/ prefix in media_files, got ${mediaRow.file_path}`)
    }

    // 11. Verify credit deduction
    const uploadLedger = await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.upload_deducted' }, { timeoutMs: 30000 })
    if (uploadLedger.amount !== -1) throw new Error(`Expected amount=-1, got ${uploadLedger.amount}`)

    const finalBalance = await getCreditBalance(user.id)
    console.log(`  Final credit balance: ${finalBalance}`)
    if (finalBalance !== 1) throw new Error(`Expected final balance=1, got ${finalBalance}`)

    // 12. Verify download works with permanent path
    console.log(`  Testing download with permanent path...`)
    const dlRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/download-intent`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
      body: JSON.stringify({ file_path: mediaRow.file_path })
    })
    const { request_id: dlRequestId } = await dlRes.json()

    const dlNotif = await waitForNotification(user.id, 'media.download_ready', { timeoutMs: 30000 })
    const dlPayload = JSON.parse(dlNotif.payload)
    if (!dlPayload.download_url) throw new Error('Missing download_url in download_ready')
    console.log(`  Download URL generated successfully for permanent path`)

    console.log(`  Move saga verified: uploads/ → files/, credit deducted, download works`)

  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
