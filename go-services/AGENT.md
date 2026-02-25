# Go Services — Agent Guidelines

## Conventions

**Config:** Use `spf13/viper` mapped to `Config{}` structs — never raw `os.Getenv` calls. Exception: `api-gateway/main.go` uses `os.Getenv` for SASL fields. Viper requires `SetDefault()` for all Config struct fields or they won't bind from env vars.

**Libraries:** `hamba/avro`, `twmb/franz-go`, `lib/pq` — no ORMs. Kafka consumer failures must crash the pod (`log.Fatalf`) to prevent offset advancement and silent data loss.

**Docker rebuilds:** `docker compose restart` does NOT recompile. Always `docker compose build --no-cache <service> && docker compose up -d <service>`.

**Dockerfiles — keep build context in sync:** When adding, renaming, or moving source files, verify the Dockerfile (and `.dockerignore`) copies them into the build context. Prefer glob patterns (`COPY *.go ./`) over hardcoded filenames. Missing files only surface at Docker build time, not during local builds.

## JWT Authentication

The API Gateway validates JWTs via shared `jwtKeyFunc` supporting HS256 (`SUPABASE_JWT_SECRET`) and ES256 (`SUPABASE_JWKS_URL`). The `authenticateRequest()` helper in `media_deps.go` is shared across upload, download, and delete handlers.

## Media Endpoints (api-gateway)

**Upload** (`upload_handler.go`): Generates presigned PUT URLs. Raw files never traverse the gateway. Credits deducted on upload completion (async MinIO webhook), not at URL request time. Webhook handler at `/webhooks/media-upload` (no auth — network-isolated). See [learnings.md](../learnings.md) for MinIO gotchas.

**Download** (`download_handler.go`): `POST /api/v1/media/download-url` — verifies ownership via `PostgresFileStore.GetFileMetadata()` (queries `media_files` WHERE `status = 'active'`), generates presigned GET URL, emits `FileDownloaded` to `public.media.download.events`.

**Delete** (`delete_handler.go`): `POST /api/v1/media/delete` — verifies ownership, soft-deletes (`UPDATE media_files SET status = 'deleted'`), emits `FileDeleted` to `public.media.delete.events`. No credit refund.

**Shared deps** (`media_deps.go`): `FileStore` interface (`VerifyOwnership`, `SoftDelete`, `GetFileMetadata`), `FullURLSigner` interface, `EventProducer` type, `authenticateRequest()` helper. `upload_deps.go` has `PostgresFileStore`, `MinioURLSigner`, and `initMediaHandlers()` — all three handlers share a single DB pool and MinIO client.

**Endpoints wired in `main.go`** (not via routes.yaml). Kafka events emitted via `produceEventDirect()`.

**Env vars:** `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`, `SUPABASE_DB_URL`. Webhook setup: `scripts/setup-minio-webhook.sh` (must run after MinIO restart).

## Credit Economy

`credit_ledger` (append-only, INSERT-only) + `user_credit_balances` (aggregated SUM view). Signup grants +2 credits, each upload costs 1 credit. Event type: `credit.balance_changed`. `media_files` uses a `status` column for soft-delete.
