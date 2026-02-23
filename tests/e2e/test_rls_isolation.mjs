import { signUpTestUser, signInTestUser, waitForNotification, cleanupUser, createUserClient, runTest, sleep, API_GATEWAY_URL } from './helpers.mjs'

const passed = await runTest('RLS isolation — users cannot see other users\' identity or direct message events', async () => {
  // Create two test users
  const userA = await signUpTestUser()
  console.log(`  Created user A: ${userA.email} (${userA.user.id})`)

  const userB = await signUpTestUser()
  console.log(`  Created user B: ${userB.email} (${userB.user.id})`)

  try {
    // Wait for signup notifications to be processed by Flink
    await waitForNotification(userA.user.id, 'identity.signup')
    console.log('  User A signup notification confirmed')
    await waitForNotification(userB.user.id, 'identity.signup')
    console.log('  User B signup notification confirmed')

    // Sign in as user B and query user_notifications through RLS
    await sleep(3000)
    const sessionB = await signInTestUser(userB.email, userB.password)
    const clientB = createUserClient(sessionB.access_token)

    // User B should see their own identity events
    const { data: ownEvents, error: ownErr } = await clientB
      .from('user_notifications')
      .select('*')
      .eq('user_id', userB.user.id)
      .eq('event_type', 'identity.signup')
    if (ownErr) throw new Error(`Query own events failed: ${ownErr.message}`)
    if (ownEvents.length === 0) throw new Error('User B cannot see their own signup event')
    console.log(`  User B sees own signup event: OK`)

    // User B should NOT see user A's identity events
    const { data: otherEvents, error: otherErr } = await clientB
      .from('user_notifications')
      .select('*')
      .eq('user_id', userA.user.id)
      .eq('event_type', 'identity.signup')
    if (otherErr) throw new Error(`Query other events failed: ${otherErr.message}`)
    if (otherEvents.length > 0) {
      throw new Error(`RLS VIOLATION: User B can see user A's identity.signup event (found ${otherEvents.length} rows)`)
    }
    console.log(`  User B cannot see user A's signup event: OK (RLS enforced)`)

    // User B should NOT see any identity events for user A (login, signup, signout)
    const { data: allOtherEvents, error: allErr } = await clientB
      .from('user_notifications')
      .select('*')
      .eq('user_id', userA.user.id)
      .in('event_type', ['identity.signup', 'identity.login', 'identity.signout'])
    if (allErr) throw new Error(`Query all other identity events failed: ${allErr.message}`)
    if (allOtherEvents.length > 0) {
      throw new Error(`RLS VIOLATION: User B can see ${allOtherEvents.length} identity event(s) belonging to user A`)
    }
    console.log(`  User B cannot see any of user A's identity events: OK`)

    // --- Direct message isolation ---
    // User A sends a direct message to themselves (not to user B)
    const sessionA = await signInTestUser(userA.email, userA.password)
    const testMessage = `rls-dm-test-${Date.now()}`
    const dmPayload = {
      user_id: userA.user.id,
      email: userA.email,
      message: testMessage,
      timestamp: new Date().toISOString(),
      visibility: 'direct',
      recipient_id: userA.user.id
    }

    const res = await fetch(`${API_GATEWAY_URL}/api/v1/events/message`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${sessionA.access_token}`
      },
      body: JSON.stringify(dmPayload)
    })
    if (!res.ok) throw new Error(`API Gateway returned ${res.status}: ${await res.text()}`)
    console.log('  User A sent a direct message to self')

    // Wait for the message to arrive
    await waitForNotification(userA.user.id, 'user.message')
    console.log('  User A direct message confirmed in DB')

    // User B should NOT see user A's direct message
    const { data: dmEvents, error: dmErr } = await clientB
      .from('user_notifications')
      .select('*')
      .eq('user_id', userA.user.id)
      .eq('event_type', 'user.message')
    if (dmErr) throw new Error(`Query direct messages failed: ${dmErr.message}`)
    if (dmEvents.length > 0) {
      throw new Error(`RLS VIOLATION: User B can see user A's direct message (found ${dmEvents.length} rows)`)
    }
    console.log(`  User B cannot see user A's direct message: OK (RLS enforced)`)
  } finally {
    await cleanupUser(userA.user.id)
    await cleanupUser(userB.user.id)
  }
})

process.exit(passed ? 0 : 1)
