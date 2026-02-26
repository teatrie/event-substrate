import {
  signUpTestUser, waitForRow,
  cleanupUser, runTest, sleep
} from './helpers.mjs'
import { execSync } from 'child_process'

const RPK_AUTH = '--brokers localhost:9092 --user flink-processor --password flink-processor-local-pw --sasl-mechanism SCRAM-SHA-256'
const RPK_SUPERUSER = '--brokers localhost:9092 --user superuser --password superuser-local-pw --sasl-mechanism SCRAM-SHA-256'

const passed = await runTest('Delete Retry Health: topics + schemas + consumer offset advance', async () => {
  // ── Step 1: Verify topics exist ──────────────────────────────────
  console.log('  Checking topics exist...')
  const topicList = execSync(
    `docker exec redpanda rpk topic list ${RPK_AUTH}`,
    { encoding: 'utf-8', timeout: 10000 }
  )

  const requiredTopics = [
    'internal.media.delete.retry',
    'internal.media.delete.dead-letter'
  ]
  for (const topic of requiredTopics) {
    if (!topicList.includes(topic)) {
      throw new Error(`Topic "${topic}" not found in rpk topic list`)
    }
    console.log(`    ${topic} exists`)
  }

  // ── Step 2: Verify schemas registered ────────────────────────────
  console.log('  Checking schemas registered in Schema Registry...')
  const requiredSubjects = [
    'internal.media.delete.retry-value',
    'internal.media.delete.dead-letter-value'
  ]
  for (const subject of requiredSubjects) {
    const result = execSync(
      `curl -sf http://localhost:8081/subjects/${subject}/versions/latest`,
      { encoding: 'utf-8', timeout: 10000 }
    )
    const parsed = JSON.parse(result)
    if (!parsed.schema) {
      throw new Error(`Subject "${subject}" has no schema`)
    }
    console.log(`    ${subject} registered (id=${parsed.id})`)
  }

  // ── Step 3: Verify handler consumes (offset advance) ─────────────
  console.log('  Creating test user for event payload...')
  const { user, email } = await signUpTestUser()
  console.log(`    Created test user: ${email} (${user.id})`)

  try {
    // Wait for credit initialization so user is fully set up
    await waitForRow('credit_ledger', { user_id: user.id, event_type: 'credit.signup_bonus' }, { timeoutMs: 45000 })
    console.log('    Credit initialized')

    // Snapshot consumer group offset before producing
    console.log('  Capturing consumer group offset before produce...')
    let offsetBefore = null
    try {
      const groupInfo = execSync(
        `docker exec redpanda rpk group describe media-service-consumer ${RPK_SUPERUSER}`,
        { encoding: 'utf-8', timeout: 10000 }
      )
      // Find the line for internal.media.delete.retry and extract committed offset
      for (const line of groupInfo.split('\n')) {
        if (line.includes('internal.media.delete.retry')) {
          const parts = line.trim().split(/\s+/)
          // rpk group describe columns: TOPIC PARTITION CURRENT-OFFSET LOG-END-OFFSET LAG MEMBER-ID ...
          offsetBefore = parseInt(parts[2], 10)
          break
        }
      }
    } catch {
      // Consumer group may not exist yet if no messages have been consumed
    }
    console.log(`    Offset before: ${offsetBefore ?? 'N/A (topic not yet in group)'}`)

    // Produce a FileDeleteRetry event with retry_count=0 and a fake file_path
    const uniqueId = Math.random().toString(36).substring(2, 8)
    const requestId = `delete-health-${uniqueId}`
    const event = JSON.stringify({
      user_id: user.id,
      file_path: `files/${user.id}/nonexistent-${uniqueId}.png`,
      file_name: `nonexistent-${uniqueId}.png`,
      media_type: 'image/png',
      file_size: 512,
      request_id: requestId,
      retry_count: 0,
      failed_at: new Date().toISOString()
    })

    console.log(`  Producing FileDeleteRetry event (retry_count=0, request_id=${requestId})...`)
    execSync(
      `echo '${event}' | docker exec -i redpanda rpk topic produce internal.media.delete.retry ` +
      `--schema-id=topic ${RPK_AUTH} ` +
      `-k '${requestId}'`,
      { stdio: 'pipe', timeout: 15000 }
    )
    console.log('    Event produced')

    // Wait for the consumer to process the message
    console.log('  Waiting 5s for consumer to process...')
    await sleep(5000)

    // Check consumer group offset advanced
    console.log('  Checking consumer group offset after consume...')
    const groupInfoAfter = execSync(
      `docker exec redpanda rpk group describe media-service-consumer ${RPK_SUPERUSER}`,
      { encoding: 'utf-8', timeout: 10000 }
    )

    let offsetAfter = null
    for (const line of groupInfoAfter.split('\n')) {
      if (line.includes('internal.media.delete.retry')) {
        const parts = line.trim().split(/\s+/)
        offsetAfter = parseInt(parts[2], 10)
        break
      }
    }

    if (offsetAfter === null) {
      throw new Error('Consumer group has no committed offset for internal.media.delete.retry after produce')
    }
    console.log(`    Offset after: ${offsetAfter}`)

    if (offsetBefore !== null && offsetAfter <= offsetBefore) {
      throw new Error(`Consumer offset did not advance: before=${offsetBefore}, after=${offsetAfter}`)
    }

    // If offsetBefore was null (first message ever), offsetAfter >= 1 means it consumed
    if (offsetBefore === null && offsetAfter < 1) {
      throw new Error(`Expected offset >= 1 after first consume, got ${offsetAfter}`)
    }

    console.log('  Delete retry pipeline health verified: topics + schemas + consumer active')

  } finally {
    await cleanupUser(user.id)
  }
})

process.exit(passed ? 0 : 1)
