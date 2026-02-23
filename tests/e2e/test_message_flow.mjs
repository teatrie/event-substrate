import { signUpTestUser, signInTestUser, waitForNotification, cleanupUser, runTest, sleep, API_GATEWAY_URL } from './helpers.mjs'

const passed = await runTest('Message POST → user_notifications (user.message)', async () => {
  // Create and login a test user
  const { email, password, user } = await signUpTestUser()
  console.log(`  Created test user: ${email} (${user.id})`)

  try {
    await sleep(3000)
    const session = await signInTestUser(email, password)
    console.log('  Logged in successfully')

    // Post a message event through the API Gateway
    const testMessage = `e2e-test-${Date.now()}`
    const payload = {
      user_id: user.id,
      email: email,
      message: testMessage,
      timestamp: new Date().toISOString(),
      visibility: 'direct',
      recipient_id: user.id
    }

    const res = await fetch(`${API_GATEWAY_URL}/api/v1/events/message`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${session.access_token}`
      },
      body: JSON.stringify(payload)
    })

    if (!res.ok) {
      const body = await res.text()
      throw new Error(`API Gateway returned ${res.status}: ${body}`)
    }
    console.log('  Message posted to API Gateway')

    const notif = await waitForNotification(user.id, 'user.message')
    console.log(`  Notification received: ${JSON.stringify(notif.payload)}`)

    const notifPayload = JSON.parse(notif.payload)
    if (notifPayload.message !== testMessage) {
      throw new Error(`Message mismatch: expected "${testMessage}", got "${notifPayload.message}"`)
    }
  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
