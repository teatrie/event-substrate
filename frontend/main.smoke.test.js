/**
 * Smoke tests that verify the frontend HTML contains the expected DOM elements
 * and that main.js actually imports the media module.
 *
 * These tests catch "dead module" bugs where backend logic exists (media.js)
 * but the UI (index.html) and wiring (main.js) are missing.
 */
import { describe, it, expect, beforeAll } from 'vitest'
import { readFileSync } from 'fs'
import { resolve, dirname } from 'path'
import { fileURLToPath } from 'url'

const __dirname = dirname(fileURLToPath(import.meta.url))
const htmlContent = readFileSync(resolve(__dirname, 'index.html'), 'utf-8')
const mainJsContent = readFileSync(resolve(__dirname, 'main.js'), 'utf-8')

// ---------------------------------------------------------------------------
// HTML smoke tests — verify required DOM element IDs exist in index.html
// ---------------------------------------------------------------------------
describe('index.html DOM elements', () => {
    describe('auth view', () => {
        const authIds = ['auth-view', 'auth-form', 'email', 'password', 'submit-btn', 'toggle-mode']
        for (const id of authIds) {
            it(`contains #${id}`, () => {
                expect(htmlContent).toContain(`id="${id}"`)
            })
        }
    })

    describe('home view', () => {
        const homeIds = ['home-view', 'welcome-message', 'message-form', 'message-input', 'send-message-btn', 'notifications-list', 'logout-btn']
        for (const id of homeIds) {
            it(`contains #${id}`, () => {
                expect(htmlContent).toContain(`id="${id}"`)
            })
        }
    })

    describe('media upload section', () => {
        const mediaIds = ['media-section', 'credit-badge', 'credit-count', 'upload-form', 'file-input', 'upload-btn', 'upload-label', 'upload-dropzone', 'upload-status']
        for (const id of mediaIds) {
            it(`contains #${id}`, () => {
                expect(htmlContent).toContain(`id="${id}"`)
            })
        }
    })

    describe('media browser section', () => {
        const browserIds = ['media-browser', 'media-list']
        for (const id of browserIds) {
            it(`contains #${id}`, () => {
                expect(htmlContent).toContain(`id="${id}"`)
            })
        }
    })
})

// ---------------------------------------------------------------------------
// main.js import assertions — verify modules are actually wired up
// ---------------------------------------------------------------------------
describe('main.js module wiring', () => {
    it('imports from media.js', () => {
        expect(mainJsContent).toContain("from './media.js'")
    })

    const requiredImports = [
        'requestUploadUrl',
        'uploadFileToStorage',
        'fetchUserFiles',
        'formatFileSize',
        'isAllowedMediaType',
        'getMediaCategory',
        'InsufficientCreditsError',
    ]

    for (const fn of requiredImports) {
        it(`imports ${fn} from media.js`, () => {
            expect(mainJsContent).toContain(fn)
        })
    }

    it('references the upload-form element', () => {
        expect(mainJsContent).toContain('upload-form')
    })

    it('references the media-list element', () => {
        expect(mainJsContent).toContain('media-list')
    })

    it('references the credit-badge element', () => {
        expect(mainJsContent).toContain('credit-badge')
    })

    it('queries user_credit_balances for credits', () => {
        expect(mainJsContent).toContain('user_credit_balances')
    })
})
