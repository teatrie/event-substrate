import { signUpTestUser, signInTestUser, waitForRow, getCreditBalance, cleanupUser, runTest, API_GATEWAY_URL } from './helpers.mjs'

const passed = await runTest('Media upload → presigned URL → MinIO → media_files', async () => {
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

    // 4. Request a presigned upload URL from the API Gateway
    const uploadReq = {
      file_name: 'e2e-test-image.png',
      media_type: 'image/png',
      file_size: 1024
    }

    const urlRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/upload-url`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify(uploadReq)
    })

    if (urlRes.status === 404 || urlRes.status === 501) {
      throw new Error(`Upload endpoint not available (status ${urlRes.status}). Is the upload handler wired in main.go?`)
    }
    if (urlRes.status !== 200) {
      const errBody = await urlRes.text()
      throw new Error(`Expected 200 from upload-url, got ${urlRes.status}: ${errBody}`)
    }

    const { upload_url, file_path, expires_in } = await urlRes.json()
    console.log(`  Got presigned URL, file_path=${file_path}, expires_in=${expires_in}s`)

    if (!upload_url) throw new Error('Missing upload_url in response')
    if (!file_path.startsWith('uploads/')) throw new Error(`Unexpected file_path: ${file_path}`)
    if (!file_path.includes(user.id)) throw new Error(`file_path should contain user_id: ${file_path}`)

    // 5. Upload a small test file directly to MinIO using the presigned URL
    //    The gateway signs URLs against host.docker.internal:9000 (K8s internal).
    //    Node's fetch (undici) strips custom Host headers, so we use http.request
    //    to connect to localhost:9000 while sending Host: host.docker.internal:9000
    //    to satisfy the S3v4 signature verification.
    const testFileContent = Buffer.from('PNG_E2E_TEST_DATA_' + Date.now())
    const parsedUrl = new URL(upload_url)
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
      const errBody = await putRes.text()
      throw new Error(`PUT to presigned URL failed (${putRes.status}): ${errBody}`)
    }
    console.log(`  File uploaded to MinIO (${testFileContent.length} bytes)`)

    // 6. Wait for Flink to process the upload event (MinIO webhook → Kafka → Flink → credit_ledger + media_files)
    //    This tests the full pipeline: MinIO notification → media_webhook_handler → Avro → Kafka → Flink SQL
    console.log(`  Waiting for Flink to process upload event...`)

    const mediaRow = await waitForRow('media_files', { user_id: user.id, file_name: 'e2e-test-image.png' }, { timeoutMs: 60000 })
    console.log(`  media_files entry: file_name=${mediaRow.file_name}, file_size=${mediaRow.file_size}, status=${mediaRow.status}`)

    if (mediaRow.status !== 'active') throw new Error(`Expected status='active', got ${mediaRow.status}`)
    if (mediaRow.media_type !== 'image/png') throw new Error(`Expected media_type='image/png', got ${mediaRow.media_type}`)

    // 7. Verify credits remain unchanged (sync path does not deduct — use saga for credit-gated uploads)
    const finalBalance = await getCreditBalance(user.id)
    console.log(`  Final credit balance: ${finalBalance} (unchanged — sync path, no credit deduction)`)
    if (finalBalance !== 2) throw new Error(`Expected balance=2 (unchanged), got ${finalBalance}`)

  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
