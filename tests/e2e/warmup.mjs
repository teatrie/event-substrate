/**
 * Flink pipeline warmup — canary signup + canary upload to verify the full pipeline
 * is processing before running the E2E test suite. Waits up to 3 minutes for Flink
 * taskmanagers to finish initializing after a fresh deployment.
 *
 * The canary upload exercises the entire upload pipeline through at least one checkpoint
 * cycle: credit check (PyFlink) → move saga (PyFlink) → SQL runner (Flink SQL) → media_files.
 * Without this, test_media_upload.mjs can time out on cold start.
 */
import {
  signUpTestUser, signInTestUser, waitForNotification, waitForRow,
  cleanupUser, API_GATEWAY_URL
} from './helpers.mjs'

const WARMUP_TIMEOUT_MS = 180_000 // 3 minutes — Flink can take ~2 min after fresh deploy

console.log('Warming up Flink pipeline (canary signup + upload)...')
const start = Date.now()

const { user, email, password } = await signUpTestUser()

try {
  // Phase 1: Canary signup — warms up identity + credit Flink jobs
  await waitForNotification(user.id, 'identity.signup', { timeoutMs: WARMUP_TIMEOUT_MS, pollMs: 2000 })
  const signupElapsed = ((Date.now() - start) / 1000).toFixed(1)
  console.log(`  Signup pipeline ready (${signupElapsed}s).`)

  // Phase 2: Canary upload — warms up credit check, move saga, and SQL runner checkpoints
  await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.signup_bonus' }, { timeoutMs: WARMUP_TIMEOUT_MS })
  const session = await signInTestUser(email, password)
  const token = session.access_token

  const intentRes = await fetch(`${API_GATEWAY_URL}/api/v1/media/upload-intent`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'Authorization': `Bearer ${token}` },
    body: JSON.stringify({ file_name: 'warmup-canary.png', media_type: 'image/png', file_size: 64 })
  })

  if (intentRes.status !== 202) {
    console.log(`  WARN: upload-intent returned ${intentRes.status}, skipping upload warmup`)
  } else {
    const uploadReadyNotif = await waitForNotification(user.id, 'media.upload_ready', { timeoutMs: WARMUP_TIMEOUT_MS, pollMs: 2000 })
    const { upload_url: uploadUrl } = JSON.parse(uploadReadyNotif.payload)

    // PUT a tiny file to MinIO (triggers webhook → move saga → media_files)
    const parsedUrl = new URL(uploadUrl)
    const http = await import('http')
    const content = Buffer.from('WARMUP_CANARY')

    await new Promise((resolve, reject) => {
      const req = http.default.request({
        method: 'PUT',
        hostname: '127.0.0.1',
        port: parsedUrl.port || 9000,
        path: parsedUrl.pathname + parsedUrl.search,
        headers: {
          'Content-Type': 'image/png',
          'Content-Length': content.length,
          'Host': `${parsedUrl.hostname}:${parsedUrl.port || 9000}`
        }
      }, (res) => {
        res.on('data', () => {})
        res.on('end', () => resolve())
      })
      req.on('error', reject)
      req.write(content)
      req.end()
    })

    // Wait for the media_files row — this confirms the full upload pipeline is warm
    await waitForRow('media_files', { user_id: user.id, file_name: 'warmup-canary.png' }, { timeoutMs: WARMUP_TIMEOUT_MS, pollMs: 2000 })
    const uploadElapsed = ((Date.now() - start) / 1000).toFixed(1)
    console.log(`  Upload pipeline ready (${uploadElapsed}s).`)
  }

  const totalElapsed = ((Date.now() - start) / 1000).toFixed(1)
  console.log(`Pipeline ready (${totalElapsed}s).`)
} catch (err) {
  const elapsed = ((Date.now() - start) / 1000).toFixed(1)
  console.error(`Pipeline warmup FAILED after ${elapsed}s: ${err.message}`)
  console.error('Flink may not be running. Check: kubectl get flinkdeployments -A')
  await cleanupUser(user.id)
  process.exit(1)
} finally {
  await cleanupUser(user.id)
}
