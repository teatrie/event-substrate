import { signUpTestUser, signInTestUser, waitForNotification, cleanupUser, runTest, sleep, supabase } from './helpers.mjs'

const passed = await runTest('Signout → user_notifications (identity.signout)', async () => {
  // Create and login a test user
  const { email, password, user } = await signUpTestUser()
  console.log(`  Created test user: ${email} (${user.id})`)

  try {
    await sleep(3000)
    await signInTestUser(email, password)
    console.log('  Logged in successfully')

    // Now sign out
    await sleep(1000)
    const { error } = await supabase.auth.signOut()
    if (error) throw new Error(`Signout failed: ${error.message}`)
    console.log('  Signed out successfully')

    const notif = await waitForNotification(user.id, 'identity.signout')
    console.log(`  Notification received: ${JSON.stringify(notif.payload)}`)

    const payload = JSON.parse(notif.payload)
    if (!payload.email) throw new Error('Missing email in notification payload')
  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
