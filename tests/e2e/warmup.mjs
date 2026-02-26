/**
 * Flink pipeline warmup — canary signup to verify the full pipeline is processing
 * before running the E2E test suite. Waits up to 3 minutes for Flink taskmanagers
 * to finish initializing after a fresh deployment.
 */
import { signUpTestUser, waitForNotification, cleanupUser } from './helpers.mjs'

const WARMUP_TIMEOUT_MS = 180_000 // 3 minutes — Flink can take ~2 min after fresh deploy

console.log('Warming up Flink pipeline (canary signup)...')
const start = Date.now()

const { user } = await signUpTestUser()

try {
  await waitForNotification(user.id, 'identity.signup', { timeoutMs: WARMUP_TIMEOUT_MS, pollMs: 2000 })
  const elapsed = ((Date.now() - start) / 1000).toFixed(1)
  console.log(`Pipeline ready (${elapsed}s).`)
} catch (err) {
  const elapsed = ((Date.now() - start) / 1000).toFixed(1)
  console.error(`Pipeline warmup FAILED after ${elapsed}s: ${err.message}`)
  console.error('Flink may not be running. Check: kubectl get flinkdeployments -A')
  await cleanupUser(user.id)
  process.exit(1)
} finally {
  await cleanupUser(user.id)
}
