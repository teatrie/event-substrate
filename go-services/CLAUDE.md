# Go Services — Agent Guidelines

## Conventions

**Config:** Use `spf13/viper` mapped to `Config{}` structs — never raw `os.Getenv` calls. Exception: `api-gateway/main.go` uses `os.Getenv` for SASL fields. Viper requires `SetDefault()` for all Config struct fields or they won't bind from env vars.

**Libraries:** `hamba/avro`, `twmb/franz-go`, `lib/pq` — no ORMs. Kafka consumer failures must crash the pod (`log.Fatalf`) to prevent offset advancement and silent data loss.

**Docker rebuilds:** `docker compose restart` does NOT recompile. Always `docker compose build --no-cache <service> && docker compose up -d <service>`.

**K8s imagePullPolicy:** All local dev manifests must use `imagePullPolicy: Never`. OrbStack shares the Docker daemon with K8s — `Never` tells K8s to use locally-built images directly without attempting registry pulls. `IfNotPresent` can silently use stale cached images after rebuilds, and `Always` fails because local images don't exist in any registry.

**Dockerfiles — keep build context in sync:** When adding, renaming, or moving source files, verify the Dockerfile (and `.dockerignore`) copies them into the build context. Prefer glob patterns (`COPY *.go ./`) over hardcoded filenames. Missing files only surface at Docker build time, not during local builds. When changing `go.mod` (adding dependencies, running `go mod tidy`, bumping the `go` directive), verify the Dockerfile base image satisfies the new Go version requirement. Local `go build` uses the host toolchain and won't catch version mismatches with the container.

## JWT Authentication

The API Gateway validates JWTs via shared `jwtKeyFunc` supporting HS256 (`SUPABASE_JWT_SECRET`) and ES256 (`SUPABASE_JWKS_URL`). The `authenticateRequest()` helper in `media_deps.go` is shared across upload, download, and delete handlers.

## Media Endpoints — Sync (api-gateway, legacy)

**Upload** (`upload_handler.go`): Generates presigned PUT URLs. Raw files never traverse the gateway. Credits deducted on upload completion (async MinIO webhook), not at URL request time. Webhook handler at `/webhooks/media-upload` (no auth — network-isolated). See [learnings.md](../docs/learnings.md) for MinIO gotchas.

**Download** (`download_handler.go`): `POST /api/v1/media/download-url` — verifies ownership via `PostgresFileStore.GetFileMetadata()` (queries `media_files` WHERE `status = 'active'`), generates presigned GET URL, emits `FileDownloaded` to `public.media.download.events`.

**Delete** (`delete_handler.go`): `POST /api/v1/media/delete` — verifies ownership, soft-deletes (`UPDATE media_files SET status = 'deleted'`), emits `FileDeleted` to `public.media.delete.events`. No credit refund.

**Shared deps** (`media_deps.go`): `FileStore` interface (`VerifyOwnership`, `SoftDelete`, `GetFileMetadata`), `FullURLSigner` interface, `EventProducer` type, `authenticateRequest()` helper. `upload_deps.go` has `PostgresFileStore`, `MinioURLSigner`, and `initMediaHandlers()` — all three handlers share a single DB pool and MinIO client.

**Endpoints wired in `main.go`** (not via routes.yaml). Kafka events emitted via `produceEventDirect()`.

**Env vars:** `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`, `SUPABASE_DB_URL`. Webhook setup: `scripts/setup-minio-webhook.sh` (must run after MinIO restart).

## Media Intent Endpoints — Async (api-gateway, Upload Saga)

**Intent Handler** (`intent_handler.go`): Thin Kafka producer for all three media intents. `POST /api/v1/media/{upload,download,delete}-intent` → validates JWT, generates `request_id`, produces to `internal.media.{type}.intent` → returns `202 Accepted {request_id}`. Uses `authenticateRequest()` and `produceEventDirect()` from the existing gateway infrastructure.

## Media Service (go-services/media-service/)

Dedicated Kafka consumer service owning MinIO presigned URLs, file metadata queries, and object lifecycle. Consumes 4 topics via a `TopicRouter`:

**Upload Signing** (`upload_signing_handler.go`): Consumes `public.media.upload.approved` → generates presigned PUT URL → emits `FileUploadUrlSigned` to `public.media.upload.signed`. Uses `URLSigner` interface with `publicMinioClient` (browser-reachable URLs).

**Download Signing** (`download_signing_handler.go`): Consumes `internal.media.download.intent` → verifies ownership via `FileMetadataLookup` → generates presigned GET URL → emits `FileDownloadUrlSigned` to `public.media.download.signed` or `FileDownloadRejected` on not-found/error.

**Delete** (`delete_handler.go`): Consumes `internal.media.delete.intent` → verifies ownership → removes from MinIO (before DB) → soft-deletes DB row → emits `FileDeleted` to `public.media.delete.events` or `FileDeleteRejected`.

**Expired Cleanup** (`expired_cleanup_handler.go`): Consumes `public.media.expired.events` → best-effort MinIO `RemoveObject` (errors logged, not returned). MinIO 24h lifecycle policy is the backstop.

**Key interfaces** (`interfaces.go`): `DownloadURLSigner`, `FileMetadataLookup` (returns `*FileMetadata, nil` for not-found), `ObjectRemover`, `FileDeleter`.

**Adapters** (`adapters.go`): Concrete implementations: `minioURLSigner` (PUT + GET), `minioObjectRemover`, `dbFileStore` (implements both `FileMetadataLookup` and `FileDeleter`), `avroKafkaProducer` (Confluent wire format with schema registry caching).

**Config env vars:** `REDPANDA_BROKERS`, `SCHEMA_REGISTRY_URL`, `SUPABASE_DB_URL`, `MINIO_ENDPOINT`, `MINIO_PUBLIC_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`, `UPLOAD_URL_EXPIRY` (default 900s), `DOWNLOAD_URL_EXPIRY` (default 3600s), SASL credentials.

## Credit Economy

`credit_ledger` (append-only, INSERT-only) + `user_credit_balances` (aggregated SUM view). Signup grants +2 credits, each upload intent costs 1 credit (deducted atomically in PyFlink). Expired uploads refund +1 credit. Event type: `credit.balance_changed`. `media_files` uses a `status` column for soft-delete.
