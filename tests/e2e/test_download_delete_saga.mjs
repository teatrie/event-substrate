import {
  signUpTestUser, signInTestUser, waitForNotification, waitForRow,
  getCreditBalance, cleanupUser, runTest, sleep, API_GATEWAY_URL,
  adminClient, createUserClient
} from './helpers.mjs'

/**
 * Helper: Upload a file via the saga endpoint and wait for it to be ready in media_files.
 * Returns { filePath } — the file_path in MinIO / media_files.
 */
async function uploadViaSaga(token, userId, fileName) {
  const intentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/upload-intent`, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
      'Authorization': `Bearer ${token}`
    },
    body: JSON.stringify({ file_name: fileName, media_type: 'image/png', file_size: 1024 })
  })

  if (intentRes.status !== 202) {
    const errBody = await intentRes.text()
    throw new Error(`Expected 202 from upload-intent, got ${intentRes.status}: ${errBody}`)
  }

  const { request_id: requestId } = await intentRes.json()
  if (!requestId) throw new Error('Missing request_id in upload-intent response')
  console.log(`    upload-intent accepted, request_id=${requestId}`)

  // Wait for upload_ready notification
  const uploadReadyNotif = await waitForNotification(userId, 'media.upload_ready', { timeoutMs: 60000 })
  const payload = JSON.parse(uploadReadyNotif.payload)

  if (payload.request_id !== requestId) {
    throw new Error(`request_id mismatch in upload_ready: expected ${requestId}, got ${payload.request_id}`)
  }

  const { upload_url: uploadUrl, file_path: filePath } = payload
  console.log(`    upload_ready received, file_path=${filePath}`)

  // PUT file to MinIO presigned URL
  const testFileContent = Buffer.from('PNG_E2E_DL_DEL_SAGA_TEST_' + Date.now())
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
      res.on('end', () => resolve({ ok: res.statusCode >= 200 && res.statusCode < 300, status: res.statusCode, text: () => body }))
    })
    req.on('error', reject)
    req.write(testFileContent)
    req.end()
  })

  if (!putRes.ok) {
    throw new Error(`PUT to presigned URL failed (${putRes.status}): ${putRes.text()}`)
  }
  console.log(`    File PUT to MinIO (${testFileContent.length} bytes)`)

  // Wait for media_files row to appear (Flink processes MinIO webhook)
  // With the move-to-permanent saga, media_files.file_path is now files/... (permanent path)
  const mediaRow = await waitForRow('media_files', { user_id: userId, file_name: fileName }, { timeoutMs: 60000 })
  console.log(`    media_files row confirmed (status=active, file_path=${mediaRow.file_path})`)

  return { filePath: mediaRow.file_path }
}

const passed = await runTest('Download + Delete Saga: async intent endpoints → notifications → RLS verification', async () => {
  // 1. Sign up a fresh user (triggers Flink +2 credit via signup event)
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
    console.log(`  Signed in, got access token`)

    // 4. Upload a file via the saga (full pipeline: intent → upload_ready → PUT → media_files)
    console.log(`  Uploading file via upload saga...`)
    const { filePath } = await uploadViaSaga(token, user.id, 'e2e-dl-del-saga-test.png')

    // -------------------------------------------------------------------------
    // DOWNLOAD
    // -------------------------------------------------------------------------

    // 5. POST /api/v1/media/download-intent — async saga endpoint, returns 202 + request_id
    console.log(`  Requesting download intent for file_path=${filePath}...`)
    const downloadIntentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/download-intent`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify({ file_path: filePath })
    })

    if (downloadIntentRes.status === 404 || downloadIntentRes.status === 501) {
      throw new Error(`download-intent endpoint not available (status ${downloadIntentRes.status}). Is the intent handler wired in main.go?`)
    }
    if (downloadIntentRes.status !== 202) {
      const errBody = await downloadIntentRes.text()
      throw new Error(`Expected 202 from download-intent, got ${downloadIntentRes.status}: ${errBody}`)
    }

    const downloadIntentBody = await downloadIntentRes.json()
    const { request_id: downloadRequestId } = downloadIntentBody
    console.log(`  Got 202 Accepted, download request_id=${downloadRequestId}`)
    if (!downloadRequestId) throw new Error('Missing request_id in download-intent response')

    // 6. Wait for media.download_ready notification
    console.log(`  Waiting for media.download_ready notification...`)
    const downloadReadyNotif = await waitForNotification(user.id, 'media.download_ready', { timeoutMs: 60000 })
    const downloadReadyPayload = JSON.parse(downloadReadyNotif.payload)
    console.log(`  Received media.download_ready: request_id=${downloadReadyPayload.request_id}, file_path=${downloadReadyPayload.file_path}`)

    if (downloadReadyPayload.request_id !== downloadRequestId) {
      throw new Error(`request_id mismatch: expected ${downloadRequestId}, got ${downloadReadyPayload.request_id}`)
    }
    if (!downloadReadyPayload.download_url) throw new Error('Missing download_url in media.download_ready payload')
    if (!downloadReadyPayload.file_path) throw new Error('Missing file_path in media.download_ready payload')
    if (!downloadReadyPayload.expires_in) throw new Error('Missing expires_in in media.download_ready payload')

    // 7. Validate download_url references the correct file
    const downloadUrl = new URL(downloadReadyPayload.download_url)
    const fileName = filePath.split('/').pop()
    if (!downloadUrl.pathname.includes(fileName)) {
      throw new Error(`download_url does not reference the uploaded file (${fileName}): ${downloadReadyPayload.download_url}`)
    }
    console.log(`  download_url validated — references file correctly`)

    // -------------------------------------------------------------------------
    // DELETE
    // -------------------------------------------------------------------------

    // 8. POST /api/v1/media/delete-intent — async saga endpoint, returns 202 + request_id
    console.log(`  Requesting delete intent for file_path=${filePath}...`)
    const deleteIntentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/delete-intent`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify({ file_path: filePath })
    })

    if (deleteIntentRes.status === 404 || deleteIntentRes.status === 501) {
      throw new Error(`delete-intent endpoint not available (status ${deleteIntentRes.status}). Is the intent handler wired in main.go?`)
    }
    if (deleteIntentRes.status !== 202) {
      const errBody = await deleteIntentRes.text()
      throw new Error(`Expected 202 from delete-intent, got ${deleteIntentRes.status}: ${errBody}`)
    }

    const deleteIntentBody = await deleteIntentRes.json()
    const { request_id: deleteRequestId } = deleteIntentBody
    console.log(`  Got 202 Accepted, delete request_id=${deleteRequestId}`)
    if (!deleteRequestId) throw new Error('Missing request_id in delete-intent response')

    // 9. Wait for media.file_deleted notification
    console.log(`  Waiting for media.file_deleted notification...`)
    const fileDeletedNotif = await waitForNotification(user.id, 'media.file_deleted', { timeoutMs: 60000 })
    const fileDeletedPayload = JSON.parse(fileDeletedNotif.payload)
    console.log(`  Received media.file_deleted: file_path=${fileDeletedPayload.file_path}, file_name=${fileDeletedPayload.file_name}`)

    if (!fileDeletedPayload.file_path) throw new Error('Missing file_path in media.file_deleted payload')
    if (!fileDeletedPayload.file_name) throw new Error('Missing file_name in media.file_deleted payload')
    if (fileDeletedPayload.file_name !== 'e2e-dl-del-saga-test.png') {
      throw new Error(`Unexpected file_name in file_deleted: ${fileDeletedPayload.file_name}`)
    }

    // 10. Verify via admin client: row should exist with status='deleted' (soft-delete)
    if (adminClient) {
      await sleep(2000)  // Small delay for DB update propagation

      const { data: adminRows, error: adminErr } = await adminClient
        .from('media_files')
        .select('*')
        .eq('file_path', filePath)
        .limit(1)

      if (adminErr) throw new Error(`Admin query failed: ${adminErr.message}`)
      if (!adminRows || adminRows.length === 0) throw new Error(`Expected media_files row to still exist (soft-delete), but none found via admin client`)

      const deletedRow = adminRows[0]
      console.log(`  Admin view: file_path=${deletedRow.file_path}, status=${deletedRow.status}`)
      if (deletedRow.status !== 'deleted') throw new Error(`Expected status='deleted' via admin client, got ${deletedRow.status}`)
    } else {
      console.log(`  WARN: No admin client available — skipping admin-level soft-delete verification`)
    }

    // 11. Verify via RLS: user query should NOT see the deleted file
    const userClient = createUserClient(token)
    const { data: userRows, error: userErr } = await userClient
      .from('media_files')
      .select('*')
      .eq('file_path', filePath)
      .limit(1)

    if (userErr) throw new Error(`User RLS query failed: ${userErr.message}`)
    if (userRows && userRows.length > 0) {
      throw new Error(`Expected RLS to hide deleted file, but user query returned ${userRows.length} row(s) with status=${userRows[0].status}`)
    }
    console.log(`  RLS correctly hides deleted file from user queries`)

    // -------------------------------------------------------------------------
    // DOWNLOAD AFTER DELETE — should receive media.download_rejected
    // -------------------------------------------------------------------------

    // 12. POST download-intent for deleted file → expect either 202 + rejected notification,
    //     or 404/403 immediately if ownership check rejects at intent time.
    console.log(`  Requesting download intent for deleted file (expect rejection)...`)
    const postDeleteIntentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/download-intent`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify({ file_path: filePath })
    })

    if (postDeleteIntentRes.status === 202) {
      // Async rejection path: intent accepted, rejection comes via notification
      const postDeleteIntentBody = await postDeleteIntentRes.json()
      const { request_id: postDeleteRequestId } = postDeleteIntentBody
      console.log(`  Got 202 (async rejection expected), request_id=${postDeleteRequestId}`)

      console.log(`  Waiting for media.download_rejected notification...`)
      const downloadRejectedNotif = await waitForNotification(user.id, 'media.download_rejected', { timeoutMs: 60000 })
      const downloadRejectedPayload = JSON.parse(downloadRejectedNotif.payload)
      console.log(`  Received media.download_rejected: reason=${downloadRejectedPayload.reason}, request_id=${downloadRejectedPayload.request_id}`)

      if (!downloadRejectedPayload.reason) throw new Error('Missing reason in media.download_rejected payload')
      if (downloadRejectedPayload.reason !== 'not_found') {
        console.log(`  WARN: Expected reason='not_found', got '${downloadRejectedPayload.reason}' (acceptable)`)
      }
    } else if (postDeleteIntentRes.status === 404 || postDeleteIntentRes.status === 403) {
      // Sync rejection path: gateway immediately rejects (ownership check at intent time)
      console.log(`  Correctly received ${postDeleteIntentRes.status} for deleted file download intent`)
    } else {
      const errBody = await postDeleteIntentRes.text()
      throw new Error(`Expected 202 (async rejection) or 404/403 (sync rejection) for deleted file, got ${postDeleteIntentRes.status}: ${errBody}`)
    }

  } finally {
    await cleanupUser(user.id)
  }
})

// ---------------------------------------------------------------------------
// Test 2: after file_deleted notification arrives, file is no longer visible
// (status != 'active') in media_files.
// This catches the bug where the delete waiter matched on request_id instead
// of file_path — media.file_deleted has no request_id in its payload, only
// file_path and file_name, so matching on request_id always timed out.
// ---------------------------------------------------------------------------
const passed2 = await runTest('Delete saga: file not visible in media_files after file_deleted notification', async () => {
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

    // 4. Upload a file via the saga so we have something to delete
    console.log(`  Uploading file via upload saga...`)
    const { filePath } = await uploadViaSaga(token, user.id, 'e2e-delete-notification-test.png')
    console.log(`  File uploaded, file_path=${filePath}`)

    // 5. POST delete-intent
    console.log(`  Requesting delete intent for file_path=${filePath}...`)
    const deleteIntentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/delete-intent`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${token}`
      },
      body: JSON.stringify({ file_path: filePath })
    })

    if (deleteIntentRes.status !== 202) {
      const errBody = await deleteIntentRes.text()
      throw new Error(`Expected 202 from delete-intent, got ${deleteIntentRes.status}: ${errBody}`)
    }

    const { request_id: deleteRequestId } = await deleteIntentRes.json()
    console.log(`  Got delete request_id=${deleteRequestId}`)

    // 6. Wait for media.file_deleted notification.
    //    The notification payload has file_path and file_name but NO request_id.
    //    The waiter must match on file_path, not request_id.
    console.log(`  Waiting for media.file_deleted notification...`)
    const fileDeletedNotif = await waitForNotification(user.id, 'media.file_deleted', { timeoutMs: 60000 })
    const fileDeletedPayload = JSON.parse(fileDeletedNotif.payload)
    console.log(`  Received media.file_deleted: file_path=${fileDeletedPayload.file_path}, file_name=${fileDeletedPayload.file_name}`)

    if (!fileDeletedPayload.file_path) throw new Error('Missing file_path in media.file_deleted payload')
    if (fileDeletedPayload.file_path !== filePath) {
      throw new Error(`file_path mismatch in file_deleted: expected ${filePath}, got ${fileDeletedPayload.file_path}`)
    }

    // 7. NOW verify the file is no longer active in media_files.
    //    This is the state the frontend would see when it calls refreshMediaBrowser()
    //    after receiving the file_deleted notification.
    //    Use admin client to bypass RLS and confirm the soft-delete status.
    if (adminClient) {
      await sleep(2000)  // Small delay for DB update propagation

      const { data: adminRows, error: adminErr } = await adminClient
        .from('media_files')
        .select('*')
        .eq('file_path', filePath)
        .limit(1)

      if (adminErr) throw new Error(`Admin query failed: ${adminErr.message}`)
      if (!adminRows || adminRows.length === 0) {
        throw new Error('Expected soft-deleted row to still exist in media_files, but none found')
      }

      const row = adminRows[0]
      console.log(`  Admin view after file_deleted notification: status=${row.status}`)

      if (row.status === 'active') {
        throw new Error(`File is still status='active' after file_deleted notification — Flink delete pipeline not complete`)
      }
      console.log(`  Confirmed: file is no longer active (status=${row.status}) after file_deleted notification`)
    } else {
      console.log(`  WARN: No admin client available — skipping admin-level status verification`)
    }

    // 8. Verify RLS hides the file from user queries — this is what the frontend media browser would see
    const userClient = createUserClient(token)
    const { data: userRows, error: userErr } = await userClient
      .from('media_files')
      .select('*')
      .eq('file_path', filePath)
      .limit(1)

    if (userErr) throw new Error(`User RLS query failed: ${userErr.message}`)
    if (userRows && userRows.length > 0) {
      throw new Error(`Expected RLS to hide deleted file from user media browser, but query returned ${userRows.length} row(s) with status=${userRows[0].status}`)
    }
    console.log(`  RLS correctly hides deleted file — frontend media browser would show no entry`)

  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed && passed2 ? 0 : 1)
