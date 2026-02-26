# The Event-Driven Substrate

## High-Performance Base Stack for Real-Time Product Engineering

This is a production-grade blueprint for high-scale, event-driven applications. Built on the principles of **Domain-Driven Design (DDD)** and **Event Sourcing**, it provides the underlying "chassis" for rapidly spinning up complex product services—such as high-frequency transactional platforms (Uber) or massive-scale social streams (TikTok).

Instead of building individual features, this stack focuses on the **plumbing of high-throughput data**: strictly authenticated ingress, dynamic routing, real-time stream processing, and sub-second analytical querying.

See [architecture.md](./docs/architecture.md) for a detailed architecture diagram and data flow summary.

---

## Architecture & Tech Stack
*   **Blueprint:** Domain-Driven Design (DDD) & Event Sourcing
*   **Container Runtime:** OrbStack (Optimized for Apple Silicon, running Docker and Kubernetes)
*   **Identity & Read DB:** Supabase CLI (PostgreSQL, GoTrue, Realtime)
*   **Ingress Gateway:** Go custom microservice (`api-gateway`)
*   **Background Consumers:** Go custom microservices (`message-consumer`, `media-service`)
*   **Message Broker:** Redpanda (Kafka-compatible with native Iceberg Tiered Storage)
*   **Schema Registry:** Confluent Schema Registry (Avro)
*   **Stream Processor:** Apache Flink (Stateful SQL & Python processing)
*   **Data Lake:** MinIO (S3) + Project Nessie (Iceberg REST Catalog)
*   **Analytical Warehouse:** ClickHouse (Real-Time OLAP)
*   **Build System:** Task (go-task) with incremental change detection
*   **Monitoring:** OpenTelemetry + Prometheus + Grafana + Jaeger
*   **Data Pipeline:** Apache Spark + Airflow (Planned)

---

## Architecture Flow (The "Login Loop")
1. **Auth Action:** User authenticates or signs up via the custom Vite Frontend (`@supabase/supabase-js`).
2. **DB Trigger:** Supabase updates the `auth.users` table. PostgreSQL triggers (`pg_net` webhooks) fire JSON `POST` requests to the API Gateway at strictly authenticated internal maps (`/webhooks/login`).
3. **External Client Ingress:** The custom Vite Frontend can also fire HTTP POSTs directly to the gateway (`/api/v1/events/{topic}`) secured by Supabase JWTs.
4. **Dynamic Routing & Serialization:** The Go microservice strictly enforces topic allowlists using a hot-reloaded Docker volume (`routes.yaml` + `fsnotify`) mimicking a ConfigMap. Once validated, it extracts the topic name, serialized the JSON payload into Confluent Avro format, and routes it to Redpanda (e.g., `public.identity.login.events`).
5. **Processing & Unification (Flink in K8s):** Flink SQL consumes the JSON messages, decodes them, and processes the localized stream. It also runs a continuous `UNION ALL` across both domains to serialize a unified stream into a single `internal.platform.unified.events` Redpanda topic, which gets structurally encoded into Avro format utilizing the `PlatformEvent` Confluent schema.
6. **Egress (Flink):** Flink writes processed results to the unified `user_notifications` table via JDBC append-only sinks. Each processor inserts with a `{domain}.{entity}` event type (e.g., `identity.login`) and a JSON `payload` column. The frontend subscribes to a single `user_notifications` Realtime channel. Kafka source tables are registered centrally by `sql_runner.py` — individual SQL files only define JDBC sinks and INSERT logic.
7. **Analytics (ClickHouse):** An embedded ClickHouse instance natively hooks into the Redpanda Kafka cluster and continuously materializes the unified Avro topic into a heavily-indexed `MergeTree` native datastore to enable lightning fast analytics.

> [!CAUTION]
> **Realtime Race Condition Engine Design:** Due to the extreme low-latency processing of the local Flink and Redpanda deployment, processed stream events are often inserted back into Postgres *faster* than the Vite frontend is physically capable of establishing its `supabase_realtime` WebSocket subscription immediately following an authentication action. To prevent clients from artificially "missing" their own immediate login notifications, the frontend `main.js` is engineered to explicitly REST-prefetch the latest historical notifications from the unified `user_notifications` table concurrently while establishing the live WebSocket.

### Feature Flow (User Messaging)
1. **Action:** An authenticated user submits a text message on the Vite Frontend.
2. **Ingress:** The Vite Frontend fires a REST POST directly to the Go API Gateway route `/api/v1/events/message` accompanied by the Supabase JWT.
3. **Broker:** Go validates the JWT signature, enforces routing via `routes.yaml`, Avro-serializes the payload utilizing the `public/user/message.events.avsc` Confluent schema, and sinks it into the `public.user.message.events` Redpanda topic.
4. **Consumer:** A continuous Go background daemon (`message-consumer`) hooks into the `public.user.message.events` topic using `twmb/franz-go` and decodes the payload.
5. **Egress (Realtime):** The Consumer executes a prepared Postgres INSERT into the unified `public.user_notifications` table (event type `user.message`, JSON payload containing email and message text). Each message carries a `visibility` field (`broadcast` or `direct`) and an optional `recipient_id` for targeted delivery.
6. **Websockets:** Supabase catches the `user_notifications` insertion on its logical replication hook and fires a payload over `supabase_realtime` websockets. RLS policies scope delivery: broadcast messages reach all authenticated clients, while direct messages are only visible to the sender and the designated recipient. The frontend parses the `event_type` and `payload` fields to render the notification.

### Feature Flow (Upload Saga — Fully Async Media Upload + Credit Economy)
The media upload pipeline is a **fully async event-driven saga** spanning 5 services and 15 Kafka topics. Every state transition is a first-class event — no silent DB writes.

1. **Intent Submission:** The Frontend calls `POST /api/v1/media/upload-intent` with the Supabase JWT and file metadata (`file_name`, `media_type`, `file_size`). The API Gateway returns `202 Accepted` with a `request_id` and produces an `UploadIntent` event to `internal.media.upload.intent`.
2. **Credit Check (PyFlink):** The `credit_check_processor.py` (PyFlink DataStream) consumes the intent, atomically checks and deducts 1 credit via a conditional SQL query (`WHERE balance >= 1`), and routes to either `public.media.upload.approved` or `public.media.upload.rejected`.
3. **Presigned URL Generation (Media Service):** The `media-service` Go consumer picks up `upload.approved`, generates a presigned PUT URL via MinIO, and emits `FileUploadUrlSigned` to `public.media.upload.signed`.
4. **Notification (Flink SQL):** The `media_notification_processor.sql` consumes `upload.signed` and inserts a `media.upload_ready` notification (with `upload_url`, `file_path`, `request_id`) into `user_notifications`. On rejection, it inserts `media.upload_rejected` with the reason.
5. **Realtime Delivery:** Supabase Realtime pushes the notification to the Frontend via the existing WebSocket subscription.
6. **Browser-Direct Upload (Claim Check Pattern):** The Frontend uploads the file directly to MinIO using the presigned URL. The raw file never touches the API Gateway or Kafka.
7. **Upload Webhook + Move-to-Permanent:** MinIO fires an S3 PUT webhook to `POST /webhooks/media-upload` on the media-service (port 8090). The handler produces `UploadReceived` to `internal.media.upload.received` and triggers an async `MoveObject` from `uploads/` to `files/` prefix.
8. **File-Ready Webhook:** When the object arrives in `files/`, MinIO fires a second webhook to `POST /webhooks/file-ready`. The handler produces `FileReady` to `internal.media.file.ready`.
9. **Move Saga Processor (PyFlink):** The `move_saga_processor.py` (`KeyedCoProcessFunction`) joins `upload.received` + `file.ready` by permanent file path with a timeout. Both present → emits `UploadConfirmed` to `public.media.upload.confirmed` (triggers `media_files` INSERT, credit ledger entry, and notification). Timeout + retry < 3 → emits to `internal.media.upload.retry`. Timeout + retry >= 3 → emits to `internal.media.upload.dead-letter` (DLQ notification to user).
10. **Retry Loop:** The media-service retry handler consumes `upload.retry`, performs a synchronous `MoveObject`, and re-produces `UploadReceived` with `retry_count + 1` — re-entering the saga at step 9.
11. **TTL Expiry Saga (PyFlink):** The `ttl_expiry_processor.py` (`KeyedCoProcessFunction`) consumes both `upload.signed` and `upload.received`, keyed by `file_path`. It arms a 15-minute processing-time timer. If the upload arrives before the timer fires, the state is cleared. If not, it emits `FileUploadExpired` to `public.media.expired.events`, triggering a credit refund (+1 via `credit_balance_processor.sql`) and best-effort MinIO object removal (via `media-service`). MinIO's 24h lifecycle policy acts as a backstop.

> [!NOTE]
> **Credit Deduction Timing:** Credits are deducted **at intent time** (atomic SQL in PyFlink), not at upload completion. If the upload fails or is abandoned, the TTL saga automatically refunds the credit after 15 minutes. This ensures users are never permanently charged for incomplete uploads.

### Feature Flow (Download Saga — Async Presigned GET URL)
1. **Intent:** The Frontend calls `POST /api/v1/media/download-intent` with the JWT and `file_path`. Returns `202 Accepted` with `request_id`.
2. **Media Service:** Consumes `internal.media.download.intent`, verifies file ownership via `media_files` (active files only), generates a presigned GET URL, and emits `FileDownloadUrlSigned` to `public.media.download.signed`. If the file isn't found, emits `FileDownloadRejected`.
3. **Notification:** Flink routes `download.signed` to a `media.download_ready` notification (with `download_url`) or `download.rejected` to `media.download_rejected`.
4. **Browser Download:** The Frontend receives the URL via Realtime and opens it in a new tab.

### Feature Flow (Delete Saga — Async Soft-Delete + MinIO Removal)
1. **Intent:** The Frontend calls `POST /api/v1/media/delete-intent` with the JWT and `file_path`. Returns `202 Accepted` with `request_id`.
2. **Media Service:** Consumes `internal.media.delete.intent`, verifies ownership, removes the object from MinIO, soft-deletes the DB row (`status='deleted'`), and emits `FileDeleted` to `public.media.delete.events`. On failure, emits `FileDeleteRejected`.
3. **Notification:** Flink routes `delete.events` to `media.file_deleted` and `delete.rejected` to `media.delete_rejected`.
4. **Immediate Effect:** RLS policies filter `status = 'active'` only, so the file vanishes from all user queries.

> [!NOTE]
> **Delete Ordering:** MinIO object removal happens *before* the DB soft-delete. If MinIO fails, the file still exists and the user can retry. If the DB fails after MinIO succeeds, the object is gone but the soft-delete can be retried (idempotent).

### Additional Processing & Storage
*   **Media File Storage (MinIO / GCS):** Uploaded media files land in the `uploads/` prefix of the `media-uploads` MinIO bucket (24h lifecycle policy). The move-to-permanent saga reliably promotes them to the `files/` prefix (no lifecycle). Files are uploaded directly from the browser via presigned URLs — the API Gateway never proxies file bytes. File metadata is tracked in the `media_files` Postgres table with soft-delete via a `status` column. Allowed media types: JPEG, PNG, GIF, WebP, MP4, WebM, MPEG, WAV, OGG.
*   **Data Lake (Iceberg / MinIO / Nessie):** Redpanda tiered storage automatically materializes the `internal.platform.unified.events` and `internal.identity.login.echo` topics into S3 object storage (MinIO) as open-format Apache Iceberg tables. Project Nessie serves as the Iceberg REST Catalog.
*   **PyFlink Microservices:** Four Python Flink applications run natively in K8s: `credit_check_processor.py` (atomic credit check + deduction for upload intents), `ttl_expiry_processor.py` (dual-stream `KeyedCoProcessFunction` for upload timeout saga), `move_saga_processor.py` (move-to-permanent saga supervisor with retry + DLQ), and `echo_processor.py` for identity event echo.

---

## 🛠️ Engineering Principles: Strict Best Practices

When extending or maintaining this platform, **strict adherence to framework and language best practices** is mandatory. This ensures longevity, performance, and readability across microservices:

*   **Go Architecture Context:** Always utilize type-safe, proven methodologies. Avoid quick scripts. For example, environment configurations must always parse through structural abstraction patterns like `spf13/viper` mapped strictly to defined `Config{}` structs, rather than scattering `os.Getenv` raw calls throughout the codebase. Use standardized libraries (`hamba/avro`, `twmb/franz-go`, `lib/pq` over opaque ORMs).
*   **Docker Container Recompilation (Compiled Languages):** When modifying source code for compiled languages (such as Go) that run inside Docker containers, executing `docker compose restart <service>` is **insufficient**. You must strictly execute `docker compose build --no-cache <service>` followed by `docker compose up -d <service>` to flush the cache, recompile the binary natively, and boot the container utilizing the new code.
*   **Javascript/Vite Context:** Ensure clear asynchronous fetch boundaries, structured error handling boundaries, native DOM sanitization routines before innerHTML modification, and modular variable scopes. Avoid massive flat `main.js` files where logically splittable.
*   **SQL Context:** Write explicit, declarative schema management relying natively on intrinsic Postgres features (RLS, Log Replication realtime engines). Let the database handle access gating optimally over backend application server code.

---

## Directory Structure
- `Taskfile.yml`: Build system definition with incremental change detection for Go services.
- `avro/`: Avro schemas organized by domain (e.g., `avro/public/identity/login.events.avsc`). Directory paths map to Kafka topic names.
- `clickhouse/`: Initialization SQL scripts for the embedded analytical datastore.
- `docker-compose.yml`: Infrastructure definition (Redpanda, Schema Registry, MinIO, ClickHouse).
- `flink_jobs/`: Flink SQL scripts with `${ENV_VAR}` placeholders resolved at runtime by `sql_runner.py`. Kafka source tables are registered centrally; SQL files only define sinks and INSERT logic.
- `tests/e2e/`: End-to-end pipeline tests validating the full data flow into `user_notifications`.
- `docs/github_cicd_plan.md`: Roadmap for GitHub Actions, multisite runners, and the "Lean CI" profile.
- `docs/data_governance_plan.md`: Strategy for Polaris Catalog, Apache Ranger, and PII masking.
- `docs/data_processing_plan.md`: Blueprint for Spark on K8s and Airflow orchestration.
- `frontend/`: Vanilla Vite web application providing the sleek UI for Logins, Signups, and Media Uploads. Includes `media.js` module with upload, credit check, and file management functions.
- `pyflink_jobs/`: Python Flink scripts (`echo_processor.py`, `credit_check_processor.py`, `ttl_expiry_processor.py`, `move_saga_processor.py`).
- `go-services/`: Go microservices (`api-gateway`, `message-consumer`, `media-service`).
- `kubernetes/`: Kubernetes deployment manifests for Flink, Go services, and Flink Operator.
- `supabase/`: Database migrations and webhook configurations.
- `telemetry/`: Telemetry configuration files (Prometheus, Grafana, Loki, OpenTelemetry).

## Running the Platform

The platform uses [Task](https://taskfile.dev) (`go-task`) as its build system. Task provides **incremental builds** — Go binaries and Docker images are only rebuilt when their source files change, and Flink ConfigMaps are only re-applied when SQL or Python sources are modified.

### Prerequisites
```bash
brew install go-task
```

### First-time Setup
Installs tools (Helm, Supabase CLI, kubectl), initializes databases, auto-discovers and registers Avro schemas, creates Kafka topics, enables Iceberg, and brings up the full stack:
```bash
task init
```

### Subsequent Runs
Starts the platform after a shutdown. Only rebuilds Go Docker images if source files changed since the last build:
```bash
task start
```

### Starting the Frontend
To launch the glassmorphism web UI:
1. Copy `frontend/.env.example` to `frontend/.env` and paste your Supabase Anon Key (printed during `task init`).
2. Run the dev server:
```bash
task frontend
```

> [!NOTE]
> **Local Signup Flow**: By default, local Supabase Auth has email confirmations enabled. When you create a new account in the UI, you won't be able to log in immediately. You must either:
> 1. Manually confirm the email via the local InBucket dashboard at [http://127.0.0.1:54324](http://127.0.0.1:54324)
> 2. Disable email confirmations entirely in `supabase/config.toml` (`enable_signup = true`, `double_confirm_changes = false`).

### Shutdown
Stops Supabase, Docker Compose, and deletes Kubernetes jobs:
```bash
task shutdown
```

### All Available Tasks

**Platform lifecycle**

| Command | Description |
|---|---|
| `task init` | First-time full platform setup (installs tools, starts all services, registers schemas, deploys K8s) |
| `task start` | Start platform after a shutdown (incremental Go rebuilds, idempotent reconciliation) |
| `task shutdown` | Stop all services (K8s deployments, Docker Compose, Supabase) |
| `task purge` | Nuclear teardown — destroy all state and volumes, then run `task init` to rebuild from scratch |

**Build**

| Command | Description |
|---|---|
| `task build` | Build all Go service Docker images (change-detected — only rebuilds if source changed) |
| `task build:gateway` | Build API Gateway image only |
| `task build:consumer` | Build Message Consumer image only |
| `task build:media-service` | Build Media Service image only |
| `task build:pyflink` | Build custom PyFlink image (includes psycopg2) |

**Infrastructure & config**

| Command | Description |
|---|---|
| `task auth:setup` | Create Redpanda SASL users and ACLs (idempotent) |
| `task schemas:register` | Auto-discover and register all Avro schemas from `avro/` |
| `task topics:create` | Create Kafka topics and enable Iceberg tiered storage |
| `task minio:setup-webhook` | Configure MinIO bucket webhooks for media upload notifications |
| `task clickhouse:init` | Initialize ClickHouse tables and materialized views |

**Kubernetes**

| Command | Description |
|---|---|
| `task k8s:operators` | Install Flink Operator and KEDA (one-time) |
| `task k8s:configmaps` | Reload Flink SQL/Python ConfigMaps from source (required before redeploying Flink jobs) |
| `task k8s:flink` | Deploy all Flink jobs to Kubernetes |
| `task k8s:go-services` | Build and deploy Go microservices to K8s |

**Testing**

| Command | Description |
|---|---|
| `task test:e2e` | Run all end-to-end pipeline tests (API → Kafka → Flink → Postgres) |
| `task test:e2e:login` | Test login → user_notifications flow |
| `task test:e2e:message` | Test message POST → user_notifications flow |
| `task test:e2e:upload` | Test media upload saga → credit deduction → notification flow |
| `task test:browser` | Run Playwright browser UI tests — **requires `task frontend` running in a separate terminal first** |

**Utilities**

| Command | Description |
|---|---|
| `task frontend` | Install dependencies and start Vite dev server (required before `task test:browser`) |
| `task status` | Show status of all services (Supabase, Docker, K8s pods) |
| `task logs:gateway` | Tail API Gateway logs |
| `task logs:consumer` | Tail Message Consumer logs |
| `task logs:flink` | Tail Flink job logs |
| `task clean` | Clear build checksums (forces full rebuild on next `task start`) |

> [!TIP]
> **Incremental Builds:** Task tracks file checksums in `.task/`. When you modify a Go source file, only the affected service image is rebuilt on the next `task start`. To force a full rebuild, run `task clean` first.

> [!NOTE]
> **Browser Tests:** `task test:browser` runs Playwright against the live Vite dev server. You must have `task frontend` running in a separate terminal before starting the tests. The test suite exercises the full UI flow — login, media upload, credit display, notifications — against the real backend.

## Telemetry & Observability (Grafana + OpenTelemetry)

The local environment includes a complete OpenTelemetry (OTel) observability stack designed to perfectly mirror a production Datadog or Google Cloud stack without vendor lock-in.

### Local Dashboards
When you run `task start`, the telemetry stack boots up alongside the infrastructure:
*   **Grafana UI:** [http://localhost:3000](http://localhost:3000) (No login required)
*   **Prometheus (Metrics):** [http://localhost:9090](http://localhost:9090)
*   **Loki (Logs):** Port `3100` (queried via Grafana)
*   **Jaeger (Traces):** [http://localhost:16686](http://localhost:16686)

### How It Works
*   **Go Microservice:** Instrumented natively with OpenTelemetry. It pushes OTLP metrics to the OTel Collector at `host.docker.internal:4317`.
*   **Flink & PyFlink:** Instrumented using the native `StatsDReporter`. They push metrics to the OTel Collector via UDP on `host.docker.internal:8125`.
*   **OpenTelemetry Collector:** The central router (`otel-collector` in `docker-compose.yml`) receives the data and fans it out to Prometheus, Loki, and Jaeger.

### Migrating to Production (Datadog / Google Cloud)
When moving from local development to a cloud provider like Datadog or Google Cloud Operations:
1.  **Do not change the application code.** The Go and PyFlink apps remain completely untouched.
2.  Update the `exporters` block in your production `otel-collector-config.yaml` to use the vendor-specific exporter (e.g., `datadog` or `googlecloud`), and provide your API keys. The collector will seamlessly route identical metrics to your new vendor.
