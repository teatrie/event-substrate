import { createClient } from '@supabase/supabase-js'

const SUPABASE_URL = process.env.SUPABASE_URL || 'http://127.0.0.1:54321'
const SUPABASE_ANON_KEY = process.env.SUPABASE_ANON_KEY || ''
const SUPABASE_SERVICE_ROLE_KEY = process.env.SUPABASE_SERVICE_ROLE_KEY || ''
export const API_GATEWAY_URL = process.env.API_GATEWAY_URL || 'http://localhost:8080'

if (!SUPABASE_ANON_KEY) {
  console.error('SUPABASE_ANON_KEY is required. Run: export SUPABASE_ANON_KEY=$(supabase status -o json | jq -r .ANON_KEY)')
  process.exit(1)
}

// Client for auth operations (anon key)
export const supabase = createClient(SUPABASE_URL, SUPABASE_ANON_KEY)

// Admin client for cleanup and direct queries (service role key)
export const adminClient = SUPABASE_SERVICE_ROLE_KEY
  ? createClient(SUPABASE_URL, SUPABASE_SERVICE_ROLE_KEY, { auth: { autoRefreshToken: false, persistSession: false } })
  : null

// Generate a unique test email to avoid collisions
export function testEmail(prefix = 'test') {
  const id = Math.random().toString(36).substring(2, 8)
  return `${prefix}-${id}@e2e-test.local`
}

const TEST_PASSWORD = 'Test1234!!'

/**
 * Sign up a new test user and return the session.
 */
export async function signUpTestUser(email = testEmail()) {
  const { data, error } = await supabase.auth.signUp({ email, password: TEST_PASSWORD })
  if (error) throw new Error(`Signup failed: ${error.message}`)
  return { email, password: TEST_PASSWORD, session: data.session, user: data.user }
}

/**
 * Sign in an existing test user and return the session.
 */
export async function signInTestUser(email, password = TEST_PASSWORD) {
  const { data, error } = await supabase.auth.signInWithPassword({ email, password })
  if (error) throw new Error(`Login failed: ${error.message}`)
  return data.session
}

/**
 * Poll user_notifications until a matching row appears or timeout.
 * Returns the matched row or throws on timeout.
 */
export async function waitForNotification(userId, eventType, { timeoutMs = 30000, pollMs = 1000 } = {}) {
  const client = adminClient || supabase
  const start = Date.now()

  while (Date.now() - start < timeoutMs) {
    const { data, error } = await client
      .from('user_notifications')
      .select('*')
      .eq('user_id', userId)
      .eq('event_type', eventType)
      .order('created_at', { ascending: false })
      .limit(1)

    if (error) throw new Error(`Query failed: ${error.message}`)
    if (data && data.length > 0) return data[0]

    await sleep(pollMs)
  }

  throw new Error(`Timed out waiting for ${eventType} notification for user ${userId} after ${timeoutMs}ms`)
}

/**
 * Clean up test notifications for a user.
 */
export async function cleanupUser(userId) {
  if (!adminClient) return
  await adminClient.from('user_notifications').delete().eq('user_id', userId)
  await adminClient.auth.admin.deleteUser(userId)
}

/**
 * Create a Supabase client authenticated as a specific user.
 * Useful for testing RLS — queries go through row-level security as that user.
 */
export function createUserClient(accessToken) {
  return createClient(SUPABASE_URL, SUPABASE_ANON_KEY, {
    global: { headers: { Authorization: `Bearer ${accessToken}` } }
  })
}

export function sleep(ms) {
  return new Promise(resolve => setTimeout(resolve, ms))
}

/**
 * Remove stale test data from previous failed runs.
 * Finds auth users with @e2e-test.local emails and deletes their notifications + accounts.
 */
export async function cleanupStaleTestData() {
  if (!adminClient) {
    console.log('  WARN: No admin client — cannot clean up stale test data')
    return
  }
  const { data: { users }, error } = await adminClient.auth.admin.listUsers()
  if (error) {
    console.log(`  WARN: Failed to list users for cleanup: ${error.message}`)
    return
  }
  const staleUsers = users.filter(u => u.email && u.email.endsWith('@e2e-test.local'))
  if (staleUsers.length === 0) return
  console.log(`  Cleaning up ${staleUsers.length} stale test user(s)...`)
  for (const u of staleUsers) {
    await adminClient.from('user_notifications').delete().eq('user_id', u.id)
    await adminClient.auth.admin.deleteUser(u.id)
  }
}

/**
 * Run a test function with pass/fail reporting.
 */
export async function runTest(name, fn) {
  console.log(`\n--- ${name} ---`)
  try {
    await fn()
    console.log(`PASS: ${name}`)
    return true
  } catch (err) {
    console.error(`FAIL: ${name}`)
    console.error(`  ${err.message}`)
    return false
  }
}
