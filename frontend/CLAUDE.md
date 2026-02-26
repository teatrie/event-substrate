# Frontend — Agent Guidelines

## Conventions

Single Realtime subscription on `user_notifications`. Must REST-prefetch history concurrently with WebSocket setup — Flink processing outpaces socket negotiation.

Message payloads use `visibility: 'broadcast'` / `recipient_id: null` for broadcasts; `visibility: 'direct'` with a target UUID for private delivery.

Media upload UI in `media.js`. Download and delete in `media.js` (`requestDownloadUrl`, `deleteFile`). Delete requires a type-to-confirm modal — user must type "delete" before confirming.

Tests use **vitest**. Run: `npx vitest run`

Browser tests use **Playwright**. Run: `task test:browser` (requires `task frontend` running on port 5173).

## Key Files

- `media.js` — upload, download, delete API functions
- `main.js` — DOM wiring, media browser, delete confirmation modal, Realtime subscription
- `style.css` — glassmorphism design system
