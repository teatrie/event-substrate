import { defineConfig } from '@playwright/test'

export default defineConfig({
    testDir: '.',
    testMatch: '**/*.spec.js',

    timeout: 30_000,
    expect: { timeout: 10_000 },

    use: {
        baseURL: 'http://localhost:5173',
        browserName: 'chromium',
        headless: true,
        screenshot: 'only-on-failure',
        trace: 'retain-on-failure',
    },

    /* Do not auto-start the dev server — run `task frontend` separately */
    retries: 0,
    workers: 1,   // serial execution avoids auth state races
})
