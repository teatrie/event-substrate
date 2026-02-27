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

Dedicated Kafka consumer + HTTP webhook service owning MinIO presigned URLs, file metadata queries, object lifecycle, and move-to-permanent storage. Consumes 6 Kafka topics via a `TopicRouter` and serves 2 MinIO webhook endpoints on HTTP :8090:

**Upload Signing** (`upload_signing_handler.go`): Consumes `public.media.upload.approved` → generates presigned PUT URL → emits `FileUploadUrlSigned` to `public.media.upload.signed`. Uses `URLSigner` interface with `publicMinioClient` (browser-reachable URLs).

**Download Signing** (`download_signing_handler.go`): Consumes `internal.media.download.intent` → verifies ownership via `FileMetadataLookup` → generates presigned GET URL → emits `FileDownloadUrlSigned` to `public.media.download.signed` or `FileDownloadRejected` on not-found/error.

**Delete** (`delete_handler.go`): Consumes `internal.media.delete.intent` → verifies ownership → **soft-deletes DB row first** → emits `FileDeleted` → best-effort `RemoveObject` from MinIO. On MinIO failure: emits `FileDeleteRetry` to `internal.media.delete.retry` (retry_count=0). On ownership/DB failure: emits `FileDeleteRejected`.

**Delete Retry** (`delete_retry_handler.go`): Consumes `internal.media.delete.retry` → re-attempts `RemoveObject`. On success: silent (user already notified). On failure + retry_count < 3: re-emits with retry_count+1. On failure + retry_count >= 3: emits `FileDeleteDeadLetter` to `internal.media.delete.dead-letter` for ops cleanup.

**Expired Cleanup** (`expired_cleanup_handler.go`): Consumes `public.media.expired.events` → best-effort MinIO `RemoveObject` (errors logged, not returned). MinIO 24h lifecycle policy is the backstop.

**Upload Webhook** (`upload_webhook_handler.go`): HTTP handler at `/webhooks/media-upload` (port 8090). Receives MinIO `uploads/` prefix PUT events, produces `UploadReceived` to `internal.media.upload.received` with `permanent_path` and `retry_count=0`, triggers async `MoveObject` to `files/` prefix.

**File-Ready Webhook** (`file_ready_webhook_handler.go`): HTTP handler at `/webhooks/file-ready` (port 8090). Receives MinIO `files/` prefix PUT events, produces `FileReady` to `internal.media.file.ready`.

**Retry Handler** (`retry_handler.go`): Kafka consumer via TopicRouter for `internal.media.upload.retry`. Performs synchronous `MoveObject`, re-produces `UploadReceived` with `retry_count + 1` to re-enter the saga loop.

**ObjectMover** (`interfaces.go` + `adapters.go`): `ObjectMover` interface with `minioObjectMover` adapter. Idempotent: `CopyObject` + `RemoveObject`, returns nil if source missing but destination exists.

**Key interfaces** (`interfaces.go`): `DownloadURLSigner`, `FileMetadataLookup` (returns `*FileMetadata, nil` for not-found), `ObjectRemover`, `ObjectMover`, `FileDeleter`.

**Adapters** (`adapters.go`): Concrete implementations: `minioURLSigner` (PUT + GET), `minioObjectRemover`, `dbFileStore` (implements both `FileMetadataLookup` and `FileDeleter`), `avroKafkaProducer` (Confluent wire format with schema registry caching).

**Config env vars:** `REDPANDA_BROKERS`, `SCHEMA_REGISTRY_URL`, `SUPABASE_DB_URL`, `MINIO_ENDPOINT`, `MINIO_PUBLIC_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`, `UPLOAD_URL_EXPIRY` (default 900s), `DOWNLOAD_URL_EXPIRY` (default 3600s), SASL credentials.

## Credit Economy

`credit_ledger` (append-only, INSERT-only) + `user_credit_balances` (aggregated SUM view). Signup grants +2 credits, each upload intent costs 1 credit (deducted atomically in PyFlink). Expired uploads refund +1 credit. Event type: `credit.balance_changed`. `media_files` uses a `status` column for soft-delete.

## Observability & Instrumentation

### OpenTelemetry (OTel) Setup
Every Go service initializes OTel in `otel.go` via `initOTel()` called early in `main()`. The pattern:
```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/sdk/metric"
    "go.opentelemetry.io/otel/sdk/trace"
    "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
)

func initOTel() error {
    // Initialize trace provider → OTLP gRPC exporter
    // Initialize meter provider → OTLP gRPC exporter
    // Set global TracerProvider and MeterProvider
}
```
The `OTEL_EXPORTER_OTLP_ENDPOINT` env var (default `http://host.docker.internal:4317`) is read automatically by the OTel SDK — no manual parsing needed.

### Structured Logging (zerolog)
All services replace stdlib `log` with zerolog:
```go
var log = zerolog.New(os.Stderr).With().Timestamp().Logger()
```
This pattern:
- Outputs JSON to stderr (captured by Docker logging driver + Loki)
- `var log` shadows stdlib `log` package — this is intentional and allows gradual migration
- Includes automatic `timestamp` field in every log line
- Structured fields: `log.Info().Str("user_id", uid).Msg("user created")`

### Health Endpoints
Every Go service exposes two health check endpoints on port 8080:
- **`GET /healthz`** — Liveness probe. Always returns 200 OK. Used by K8s liveness probe to restart dead pods.
- **`GET /readyz`** — Readiness probe. Checks downstream dependencies:
  - Kafka broker connectivity (via live consumer group or producer)
  - Postgres connection pool (SELECT 1)
  - Returns 200 only when all are healthy. Used by K8s readiness probe to remove unhealthy pods from load balancer.

The health handlers DO NOT require authentication — they're internal network-only (port 8080, not exposed to frontend).

### HTTP Auto-Instrumentation
All HTTP handlers are wrapped with `otelhttp.NewHandler()`:
```go
mux.Handle("/api/v1/events/{topic}", otelhttp.NewHandler(
    http.HandlerFunc(eventHandler),
    "POST /api/v1/events/{topic}",
))
```
This auto-generates:
- `http.server.request.duration` (histogram, milliseconds)
- `http.server.active_requests` (gauge, per-operation)
- `http.server.request.body.size` (histogram, bytes)
- Trace spans with operation name as span name

### Custom Kafka Metrics
Each service exposes three custom metrics via a meter:
```go
kafkaProducer := meter.Int64Counter("kafka.producer.messages.total")
kafkaConsumer := meter.Int64Counter("kafka.consumer.messages.total")
kafkaErrors := meter.Int64Counter("kafka.consumer.errors.total")
```
Updated on each message:
- Producer: incremented when producing events to Kafka
- Consumer: incremented after successful message processing
- Errors: incremented when message processing fails

### Environment Variables
*   `OTEL_EXPORTER_OTLP_ENDPOINT` — OTel Collector OTLP gRPC endpoint (default: `http://host.docker.internal:4317`)
*   `LOG_LEVEL` — zerolog level (default: `info`, options: `debug`, `warn`, `error`)
