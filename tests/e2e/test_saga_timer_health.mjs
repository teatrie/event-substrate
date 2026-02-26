import {
  signUpTestUser, waitForNotification, waitForRow,
  cleanupUser, runTest
} from './helpers.mjs'
import { execSync } from 'child_process'

const passed = await runTest('Saga Timer Health: timer → DLQ → upload_failed notification', async () => {
  // 1. Sign up a fresh user (needed for user_id and to receive notifications)
  const { user, email } = await signUpTestUser()
  console.log(`  Created test user: ${email} (${user.id})`)

  try {
    // 2. Wait for credit initialization (standard guard)
    await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.signup_bonus' }, { timeoutMs: 45000 })
    console.log(`  Credit initialized`)

    // 3. Inject a "doomed" upload.received event via rpk
    //    retry_count=3 (>= MAX_RETRIES) so timer fire → dead-letter immediately
    const ts = new Date().toISOString()
    const uniqueId = Math.random().toString(36).substring(2, 8)
    const fileName = `fake-timer-test-${uniqueId}.png`
    const permanentPath = `files/${user.id}/fake-timer-test/${fileName}`
    const filePath = `uploads/${user.id}/fake-timer-test/${fileName}`

    const event = JSON.stringify({
      user_id: user.id,
      email: email,
      file_path: filePath,
      file_name: fileName,
      file_size: 1024,
      media_type: 'image/png',
      upload_time: ts,
      permanent_path: permanentPath,
      retry_count: 3
    })

    console.log(`  Injecting doomed upload.received (retry_count=3) via rpk...`)
    execSync(
      `echo '${event}' | docker exec -i redpanda rpk topic produce internal.media.upload.received ` +
      `--schema-id=topic ` +
      `--brokers localhost:9092 ` +
      `--user flink-processor --password flink-processor-local-pw --sasl-mechanism SCRAM-SHA-256 ` +
      `-k '${permanentPath}'`,
      { stdio: 'pipe', timeout: 15000 }
    )
    console.log(`  Event injected, key=${permanentPath}`)

    // 4. Wait for media.upload_failed notification
    //    Timer fires after MOVE_TIMEOUT_SECONDS (10s in dev) + pipeline latency
    console.log(`  Waiting for media.upload_failed notification (timer=10s + pipeline latency)...`)
    const failedNotif = await waitForNotification(user.id, 'media.upload_failed', { timeoutMs: 45000 })
    const payload = JSON.parse(failedNotif.payload)
    console.log(`  Got media.upload_failed: ${JSON.stringify(payload)}`)

    // 5. Verify notification payload
    if (payload.failure_reason !== 'max retries exceeded') {
      throw new Error(`Expected failure_reason="max retries exceeded", got "${payload.failure_reason}"`)
    }
    if (payload.retry_count !== 3) {
      throw new Error(`Expected retry_count=3, got ${payload.retry_count}`)
    }
    if (payload.file_name !== fileName) {
      throw new Error(`Expected file_name="${fileName}", got "${payload.file_name}"`)
    }

    console.log(`  Timer → DLQ → notification pipeline verified`)

  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
