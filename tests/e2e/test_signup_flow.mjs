import { signUpTestUser, waitForNotification, cleanupUser, runTest } from './helpers.mjs'

const passed = await runTest('Signup → user_notifications (identity.signup)', async () => {
  const { user } = await signUpTestUser()
  console.log(`  Created test user: ${user.email} (${user.id})`)

  try {
    const notif = await waitForNotification(user.id, 'identity.signup')
    console.log(`  Notification received: ${JSON.stringify(notif.payload)}`)

    const payload = JSON.parse(notif.payload)
    if (!payload.email) throw new Error('Missing email in notification payload')
    if (payload.email !== user.email) throw new Error(`Email mismatch: expected ${user.email}, got ${payload.email}`)
  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
