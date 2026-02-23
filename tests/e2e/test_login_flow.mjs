import { signUpTestUser, signInTestUser, waitForNotification, cleanupUser, runTest, sleep } from './helpers.mjs'

const passed = await runTest('Login → user_notifications (identity.login)', async () => {
  // First create a user
  const { email, password, user } = await signUpTestUser()
  console.log(`  Created test user: ${email} (${user.id})`)

  try {
    // Wait briefly for signup pipeline to complete, then login
    await sleep(3000)
    await signInTestUser(email, password)
    console.log('  Logged in successfully')

    const notif = await waitForNotification(user.id, 'identity.login')
    console.log(`  Notification received: ${JSON.stringify(notif.payload)}`)

    const payload = JSON.parse(notif.payload)
    if (!payload.email) throw new Error('Missing email in notification payload')
    if (payload.email !== email) throw new Error(`Email mismatch: expected ${email}, got ${payload.email}`)
  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
