import { signUpTestUser, waitForNotification, waitForRow, getCreditBalance, cleanupUser, runTest, sleep } from './helpers.mjs'

const passed = await runTest('Signup → credit_ledger (+2 credits) + credit.balance_changed notification', async () => {
  const { user } = await signUpTestUser()
  console.log(`  Created test user: ${user.email} (${user.id})`)

  try {
    // 1. Wait for the identity.signup notification (existing flow — confirms Flink is processing)
    await waitForNotification(user.id, 'identity.signup')
    console.log(`  identity.signup notification received`)

    // 2. Wait for the credit.balance_changed notification from credit_balance_processor
    const creditNotif = await waitForNotification(user.id, 'credit.balance_changed', { timeoutMs: 45000 })
    console.log(`  credit.balance_changed notification received: ${creditNotif.payload}`)

    const payload = JSON.parse(creditNotif.payload)
    if (payload.amount !== 2) throw new Error(`Expected amount=2, got ${payload.amount}`)
    if (payload.reason !== 'signup_bonus') throw new Error(`Expected reason='signup_bonus', got ${payload.reason}`)

    // 3. Verify credit_ledger entry exists
    const ledgerRow = await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.signup_bonus' })
    console.log(`  credit_ledger entry: amount=${ledgerRow.amount}, event_type=${ledgerRow.event_type}`)

    if (ledgerRow.amount !== 2) throw new Error(`Expected ledger amount=2, got ${ledgerRow.amount}`)

    // 4. Verify aggregated balance via view
    const balance = await getCreditBalance(user.id)
    console.log(`  user_credit_balances: balance=${balance}`)

    if (balance !== 2) throw new Error(`Expected balance=2, got ${balance}`)

  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
