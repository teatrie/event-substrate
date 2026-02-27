# Frontend — Agent Guidelines

## Conventions

Single Realtime subscription on `user_notifications`. Must REST-prefetch history concurrently with WebSocket setup — Flink processing outpaces socket negotiation.

Message payloads use `visibility: 'broadcast'` / `recipient_id: null` for broadcasts; `visibility: 'direct'` with a target UUID for private delivery.

Media upload UI in `media.js`. Download and delete in `media.js` (`requestDownloadUrl`, `deleteFile`). Delete requires a type-to-confirm modal — user must type "delete" before confirming.

Tests use **vitest**. Run: `npx vitest run`

Browser tests use **Playwright**. Run: `task test:browser` (requires `task frontend` running on port 5173).

## Realtime & Session Conventions

- **Credit refresh on Realtime events:** The Realtime handler must call `refreshCredits()` when `credit.balance_changed` arrives. Don't rely on the user reloading — the credit badge must update immediately.
- **Notification waiter path matching:** `waitFor()` accepts an array of keys for multi-path matching. After the move saga, register under both `files/` (completed) and `uploads/` (expired/failed) paths so the waiter resolves regardless of which saga outcome fires first.
- **Session validation:** `checkSession()` calls `getUser()` server-side. If the token is stale (e.g., user deleted after DB reset), auto-sign out. This prevents ghost sessions after `task purge`.

## Key Files

- `media.js` — upload, download, delete API functions
- `main.js` — DOM wiring, media browser, delete confirmation modal, Realtime subscription
- `style.css` — glassmorphism design system
