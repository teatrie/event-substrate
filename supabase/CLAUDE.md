# Supabase — Agent Guidelines

## Migration Conventions

Always set `REPLICA IDENTITY FULL` on tables consumed by Realtime.

The `user_notifications` table uses `payload TEXT` (not JSONB) for Flink JDBC compatibility.

Event types follow `{domain}.{entity}` convention (e.g., `identity.login`, `user.message`, `credit.balance_changed`).

Message visibility is controlled by `visibility` (`broadcast` | `direct`) and `recipient_id` (nullable UUID) columns — RLS enforces that direct messages are only visible to sender + recipient.

RLS on `media_files` filters `status = 'active'` — deleted files vanish immediately from all user queries.

## Deployment

After adding or deleting migrations: `supabase db reset`
