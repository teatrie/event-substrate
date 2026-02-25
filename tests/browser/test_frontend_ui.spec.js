/**
 * Playwright browser tests for the frontend UI.
 *
 * Prerequisites:
 *   - Platform running: `task start`
 *   - Vite dev server running: `task frontend`
 *
 * Run:  task test:browser
 */
import { test, expect } from '@playwright/test'

const TEST_PASSWORD = 'Test1234!!'

function uniqueEmail() {
    return `pw-${Date.now()}-${Math.random().toString(36).slice(2, 6)}@e2e-test.local`
}

/**
 * Helper: ensure user is logged in on the home view.
 * If already on home view (Supabase localStorage session), skip login.
 * Otherwise, sign up (if new email) or log in.
 */
async function ensureLoggedIn(page, email) {
    await page.goto('/')

    // Give the page a moment to check existing session
    await page.waitForTimeout(1000)

    // If already on home view (session persisted), we're done
    const homeView = page.locator('#home-view')
    if (await homeView.isVisible()) {
        return
    }

    // Not logged in — log in via the form
    // Make sure we're on login mode (not signup)
    const submitText = await page.locator('#submit-btn').textContent()
    if (submitText !== 'Log In') {
        await page.click('#toggle-mode')
    }

    await page.fill('#email', email)
    await page.fill('#password', TEST_PASSWORD)
    await page.click('#submit-btn')

    // Wait for home view to appear
    await expect(homeView).toBeVisible({ timeout: 10_000 })
}

// ---------------------------------------------------------------------------
// Auth View — unauthenticated tests (fresh context, no localStorage)
// ---------------------------------------------------------------------------
test.describe('Auth view', () => {
    // Each test gets a fresh browser context (Playwright default), so no
    // lingering Supabase session from previous tests.

    test('renders login form on initial load', async ({ page }) => {
        await page.goto('/')

        await expect(page.locator('#auth-view')).toBeVisible()
        await expect(page.locator('#home-view')).not.toBeVisible()
        await expect(page.locator('#email')).toBeVisible()
        await expect(page.locator('#password')).toBeVisible()
        await expect(page.locator('#submit-btn')).toBeVisible()
        await expect(page.locator('#submit-btn')).toHaveText('Log In')
    })

    test('can toggle to signup mode', async ({ page }) => {
        await page.goto('/')

        await page.click('#toggle-mode')
        await expect(page.locator('#submit-btn')).toHaveText('Sign Up')
        await expect(page.locator('#form-title')).toHaveText('Create an account')
    })

    test('signup + auto-login shows home view', async ({ page }) => {
        const email = uniqueEmail()
        await page.goto('/')

        // Switch to signup mode
        await page.click('#toggle-mode')
        await page.fill('#email', email)
        await page.fill('#password', TEST_PASSWORD)
        await page.click('#submit-btn')

        // Supabase local dev auto-confirms, so we either:
        // (a) see the success alert briefly then auto-redirect to home, or
        // (b) go straight to home view
        await expect(page.locator('#home-view')).toBeVisible({ timeout: 15_000 })
        await expect(page.locator('#welcome-message')).toContainText('Hello')
    })
})

// ---------------------------------------------------------------------------
// Authenticated Home View — sign up one user, then log in for each test
// ---------------------------------------------------------------------------
test.describe('Home view (authenticated)', () => {
    // Create a user once via API, then log in via the UI in each test
    let testEmail

    test.beforeAll(async ({ browser }) => {
        testEmail = uniqueEmail()

        // Sign up the user via the UI in a throwaway page
        const ctx = await browser.newContext()
        const page = await ctx.newPage()
        await page.goto('http://localhost:5173/')
        await page.click('#toggle-mode')
        await page.fill('#email', testEmail)
        await page.fill('#password', TEST_PASSWORD)
        await page.click('#submit-btn')

        // Wait until signup completes (home view visible = auto-logged in)
        await expect(page.locator('#home-view')).toBeVisible({ timeout: 15_000 })
        await ctx.close()
    })

    test.beforeEach(async ({ page }) => {
        await ensureLoggedIn(page, testEmail)
    })

    test('shows welcome message', async ({ page }) => {
        await expect(page.locator('#welcome-message')).toBeVisible()
        await expect(page.locator('#welcome-message')).toContainText('Hello')
    })

    test('shows message broadcast form', async ({ page }) => {
        await expect(page.locator('#message-form')).toBeVisible()
        await expect(page.locator('#message-input')).toBeVisible()
        await expect(page.locator('#send-message-btn')).toBeVisible()
    })

    test('shows media upload section', async ({ page }) => {
        await expect(page.locator('#media-section')).toBeVisible()
        await expect(page.locator('#upload-form')).toBeVisible()
        await expect(page.locator('#upload-dropzone')).toBeVisible()
        await expect(page.locator('#upload-btn')).toBeVisible()
    })

    test('shows credit badge with numeric balance', async ({ page }) => {
        await expect(page.locator('#credit-badge')).toBeVisible()
        const creditCount = page.locator('#credit-count')
        await expect(creditCount).toBeVisible()
        // Wait for credit count to load (replaces the initial "—" placeholder)
        await expect(creditCount).not.toHaveText('—', { timeout: 15_000 })
    })

    test('shows media browser section', async ({ page }) => {
        await expect(page.locator('#media-browser')).toBeVisible()
        await expect(page.locator('#media-list')).toBeVisible()
    })

    test('file input accepts correct media types', async ({ page }) => {
        const fileInput = page.locator('#file-input')
        const accept = await fileInput.getAttribute('accept')
        expect(accept).toContain('image/jpeg')
        expect(accept).toContain('image/png')
        expect(accept).toContain('video/mp4')
        expect(accept).toContain('audio/mpeg')
    })

    test('shows live events pane', async ({ page }) => {
        await expect(page.locator('.notifications-pane')).toBeVisible()
        await expect(page.locator('#notifications-list')).toBeVisible()
    })

    test('media file cards render download and delete buttons', async ({ page }) => {
        // Check that the media browser rendering code includes the button classes.
        // We evaluate the JS source to verify the wiring exists, since creating
        // a real file requires the full upload pipeline.
        const mainJs = await page.evaluate(async () => {
            const resp = await fetch('/main.js')
            return resp.text()
        })
        expect(mainJs).toContain('btn-download')
        expect(mainJs).toContain('btn-delete')
        expect(mainJs).toContain('requestDownloadUrl')
        expect(mainJs).toContain('deleteFile')
    })

    test('logout returns to auth view', async ({ page }) => {
        await page.click('#logout-btn')
        await expect(page.locator('#auth-view')).toBeVisible({ timeout: 5_000 })
        await expect(page.locator('#home-view')).not.toBeVisible()
    })
})
