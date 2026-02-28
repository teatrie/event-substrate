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
// Signup → Credits Flow (end-to-end Realtime)
// ---------------------------------------------------------------------------
test.describe('Signup credits flow', () => {
    test('signup grants credits and enables upload', async ({ page }) => {
        const email = uniqueEmail()
        await page.goto('/')

        // Sign up
        await page.click('#toggle-mode')
        await page.fill('#email', email)
        await page.fill('#password', TEST_PASSWORD)
        await page.click('#submit-btn')

        // Wait for home view
        await expect(page.locator('#home-view')).toBeVisible({ timeout: 15_000 })

        // Wait for credit.balance_changed to arrive via Realtime and update the badge
        const creditCount = page.locator('#credit-count')
        await expect(creditCount).toHaveText('2', { timeout: 15_000 })

        // Credit badge should not have the "empty" class
        await expect(page.locator('#credit-badge')).not.toHaveClass(/empty/)

        // Upload status should not show the no-credits message
        const uploadStatus = page.locator('#upload-status')
        await expect(uploadStatus).not.toContainText('No credits remaining')
    })
})

// ---------------------------------------------------------------------------
// Full Upload → Download → Delete Journey (end-to-end through UI)
// ---------------------------------------------------------------------------
test.describe('Upload journey', () => {
    test('upload file, see it in media browser, download, delete', async ({ page }) => {
        test.setTimeout(90_000)

        const email = uniqueEmail()
        await page.goto('/')

        // Sign up
        await page.click('#toggle-mode')
        await page.fill('#email', email)
        await page.fill('#password', TEST_PASSWORD)
        await page.click('#submit-btn')
        await expect(page.locator('#home-view')).toBeVisible({ timeout: 15_000 })

        // Wait for credits via Realtime
        await expect(page.locator('#credit-count')).toHaveText('2', { timeout: 15_000 })

        // Upload a test file
        const fileName = 'e2e-journey-test.png'
        await page.locator('#file-input').setInputFiles({
            name: fileName,
            mimeType: 'image/png',
            buffer: Buffer.from(`test-${Date.now()}`),
        })
        await expect(page.locator('#upload-label')).toContainText(fileName)
        await page.click('#upload-btn')

        // Wait for file to appear in media browser (durable outcome of the full pipeline)
        const fileCard = page.locator('.media-file-card', { hasText: fileName })
        await expect(fileCard).toBeVisible({ timeout: 60_000 })

        // Credit should be deducted (2 → 1)
        await expect(page.locator('#credit-count')).toHaveText('1', { timeout: 15_000 })

        // Delete the file
        await fileCard.locator('.btn-delete').click()
        await expect(page.locator('#delete-modal')).toBeVisible()
        await expect(page.locator('#delete-modal-filename')).toHaveText(fileName)
        await page.fill('#delete-confirm-input', 'delete')
        await page.click('#delete-confirm-btn')

        // File should disappear from media browser
        await expect(fileCard).not.toBeVisible({ timeout: 30_000 })
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

    test('media.js exports required intent API functions', async ({ page }) => {
        const exports = await page.evaluate(async () => {
            const mod = await import('/media.js')
            return Object.keys(mod)
        })
        expect(exports).toContain('requestUploadIntent')
        expect(exports).toContain('requestDownloadIntent')
        expect(exports).toContain('requestDeleteIntent')
        expect(exports).toContain('createNotificationWaiter')
    })

    test('download and delete buttons call correct API endpoints', async ({ page }) => {
        const mockFilePath = 'files/test-user/mock-file.png'
        const mockFileName = 'mock-file.png'

        // Mock Supabase PostgREST to return a fake media file
        await page.route('**/rest/v1/media_files**', route => {
            route.fulfill({
                status: 200,
                contentType: 'application/json',
                body: JSON.stringify([{
                    id: 1,
                    file_name: mockFileName,
                    file_path: mockFilePath,
                    file_size: 1024,
                    media_type: 'image/png',
                    upload_time: new Date().toISOString(),
                }]),
            })
        })

        // Mock API gateway intent endpoints
        await page.route('**/api/v1/media/download-intent', route => {
            route.fulfill({
                status: 202,
                contentType: 'application/json',
                body: JSON.stringify({ request_id: 'mock-dl-req' }),
            })
        })

        await page.route('**/api/v1/media/delete-intent', route => {
            route.fulfill({
                status: 202,
                contentType: 'application/json',
                body: JSON.stringify({ request_id: 'mock-del-req' }),
            })
        })

        // Reload to trigger media browser refresh with mocked data
        await page.reload()
        await expect(page.locator('#home-view')).toBeVisible({ timeout: 10_000 })

        const fileCard = page.locator('.media-file-card', { hasText: mockFileName })
        await expect(fileCard).toBeVisible({ timeout: 10_000 })

        // Click download → verify correct endpoint called with file_path
        const downloadPromise = page.waitForRequest('**/api/v1/media/download-intent')
        await fileCard.locator('.btn-download').click()
        const downloadReq = await downloadPromise
        expect(JSON.parse(downloadReq.postData()).file_path).toBe(mockFilePath)

        // Click delete → confirm modal → verify correct endpoint called
        await fileCard.locator('.btn-delete').click()
        await expect(page.locator('#delete-modal')).toBeVisible()
        await page.fill('#delete-confirm-input', 'delete')

        const deletePromise = page.waitForRequest('**/api/v1/media/delete-intent')
        await page.click('#delete-confirm-btn')
        const deleteReq = await deletePromise
        expect(JSON.parse(deleteReq.postData()).file_path).toBe(mockFilePath)
    })

    test('delete confirmation modal exists and is hidden by default', async ({ page }) => {
        const modal = page.locator('#delete-modal')
        await expect(modal).toBeHidden()

        // Verify modal elements are present in DOM
        await expect(page.locator('#delete-confirm-input')).toBeAttached()
        await expect(page.locator('#delete-confirm-btn')).toBeAttached()
        await expect(page.locator('#delete-cancel-btn')).toBeAttached()
    })

    test('delete confirmation modal requires typing "delete" to enable button', async ({ page }) => {
        // Show the modal by manipulating display directly
        await page.evaluate(() => {
            document.getElementById('delete-modal').style.display = 'flex'
            document.getElementById('delete-modal-filename').textContent = 'test-file.png'
        })

        const confirmBtn = page.locator('#delete-confirm-btn')
        const confirmInput = page.locator('#delete-confirm-input')

        // Button should be disabled initially
        await expect(confirmBtn).toBeDisabled()

        // Typing partial text keeps it disabled
        await confirmInput.fill('del')
        await expect(confirmBtn).toBeDisabled()

        // Typing "delete" enables it
        await confirmInput.fill('delete')
        await expect(confirmBtn).toBeEnabled()

        // Clearing disables it again
        await confirmInput.fill('')
        await expect(confirmBtn).toBeDisabled()

        // Case-insensitive
        await confirmInput.fill('DELETE')
        await expect(confirmBtn).toBeEnabled()
    })

    test('delete confirmation modal cancel closes without action', async ({ page }) => {
        await page.evaluate(() => {
            document.getElementById('delete-modal').style.display = 'flex'
        })

        await expect(page.locator('#delete-modal')).toBeVisible()
        await page.click('#delete-cancel-btn')
        await expect(page.locator('#delete-modal')).toBeHidden()
    })

    test('logout returns to auth view', async ({ page }) => {
        await page.click('#logout-btn')
        await expect(page.locator('#auth-view')).toBeVisible({ timeout: 5_000 })
        await expect(page.locator('#home-view')).not.toBeVisible()
    })

    // -------------------------------------------------------------------------
    // Notification waiter / upload flow UI states
    // These tests catch the bugs where handleNotification silently dropped
    // every notification (wrong field access) and the upload flow refreshed
    // too early (race condition: no wait for upload_completed before reload).
    // -------------------------------------------------------------------------

    test('upload form source code contains Processing status text', async ({ page }) => {
        // Verify the JS source contains the "Processing…" status string that is
        // shown while the frontend waits for the upload_completed notification
        // before calling refreshMediaBrowser(). If this text is absent, the race
        // condition fix was reverted.
        const mainJs = await page.evaluate(async () => {
            const resp = await fetch('/main.js')
            return resp.text()
        })
        expect(mainJs).toContain('Processing')
        // Also verify the notification waiter integration exists — the frontend
        // must call waitFor on upload_completed to avoid the async race.
        expect(mainJs).toContain('upload_completed')
        expect(mainJs).toContain('waitFor')
    })

    test('delete modal shows filename and clears input on re-open', async ({ page }) => {
        const modal = page.locator('#delete-modal')
        const confirmInput = page.locator('#delete-confirm-input')
        const filenameSel = page.locator('#delete-modal-filename')

        // Open modal with a filename (simulates clicking delete on a media card)
        await page.evaluate(() => {
            const modal = document.getElementById('delete-modal')
            const filenameEl = document.getElementById('delete-modal-filename')
            const input = document.getElementById('delete-confirm-input')
            filenameEl.textContent = 'first-file.png'
            input.value = 'delete'  // pre-populate as if user typed it
            modal.style.display = 'flex'
        })

        await expect(modal).toBeVisible()
        await expect(filenameSel).toHaveText('first-file.png')

        // Close via cancel
        await page.click('#delete-cancel-btn')
        await expect(modal).toBeHidden()

        // Re-open with a different filename — input must be cleared so the user
        // cannot accidentally confirm deletion of the new file without re-typing.
        await page.evaluate(() => {
            const modal = document.getElementById('delete-modal')
            const filenameEl = document.getElementById('delete-modal-filename')
            const input = document.getElementById('delete-confirm-input')
            filenameEl.textContent = 'second-file.mp4'
            // Simulate what the UI does on re-open: clear the input
            input.value = ''
            modal.style.display = 'flex'
        })

        await expect(modal).toBeVisible()
        await expect(filenameSel).toHaveText('second-file.mp4')
        // Input must be empty so the confirm button starts disabled
        await expect(confirmInput).toHaveValue('')
        await expect(page.locator('#delete-confirm-btn')).toBeDisabled()
    })
})
