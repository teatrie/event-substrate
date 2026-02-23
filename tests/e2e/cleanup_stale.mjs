import { cleanupStaleTestData } from './helpers.mjs'

console.log('Cleaning up stale test data from previous runs...')
await cleanupStaleTestData()
console.log('Done.')
