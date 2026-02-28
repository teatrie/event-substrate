# The Event-Driven Substrate

## High-Performance Base Stack for Real-Time Product Engineering

This is a production-grade blueprint for high-scale, event-driven applications. Built on the principles of **Domain-Driven Design (DDD)** and **Event Sourcing**, it provides the underlying "chassis" for rapidly spinning up complex product services—such as high-frequency transactional platforms (Uber) or massive-scale social streams (TikTok).

Instead of building individual features, this stack focuses on the **plumbing of high-throughput data**: strictly authenticated ingress, dynamic routing, real-time stream processing, and sub-second analytical querying.

See [architecture.md](./docs/architecture.md) for a detailed architecture diagram and data flow summary.

---

## Architecture & Tech Stack

* **Blueprint:** Domain-Driven Design (DDD) & Event Sourcing
* **Container Runtime:** OrbStack (Optimized for Apple Silicon, running Docker and Kubernetes)
* **Identity & Read DB:** Supabase CLI (PostgreSQL, GoTrue, Realtime)
* **Ingress Gateway:** Go custom microservice (`api-gateway`)
* **Background Consumers:** Go custom microservices (`message-consumer`, `media-service`)
* **Message Broker:** Redpanda (Kafka-compatible with native Iceberg Tiered Storage)
* **Schema Registry:** Confluent Schema Registry (Avro)
* **Stream Processor:** Apache Flink (Stateful SQL & Python processing)
* **Data Lake:** MinIO (S3) + Project Nessie (Iceberg REST Catalog)
* **Analytical Warehouse:** ClickHouse (Real-Time OLAP)
* **Build System:** Task (go-task) with incremental change detection
* **K8s Packaging:** Helm chart (`charts/event-substrate/`) for Go services + Spark apps
* **CI/CD:** GitHub Actions with path-filtered builds + GHCR image push
* **Monitoring:** OpenTelemetry + Prometheus + Grafana + Jaeger
* **Batch Orchestration:** Apache Airflow 3.1.7 (Helm 1.19.0 on K8s, KubernetesExecutor)
* **Batch Processing:** Apache Spark 4.0.2 (Spark Operator on K8s, Iceberg REST Catalog)
* **Data Lineage:** Marquez 0.49.0 + OpenLineage (Spark listener + Airflow provider)

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

* **Media File Storage (MinIO / GCS):** Uploaded media files land in the `uploads/` prefix of the `media-uploads` MinIO bucket (24h lifecycle policy). The move-to-permanent saga reliably promotes them to the `files/` prefix (no lifecycle). Files are uploaded directly from the browser via presigned URLs — the API Gateway never proxies file bytes. File metadata is tracked in the `media_files` Postgres table with soft-delete via a `status` column. Allowed media types: JPEG, PNG, GIF, WebP, MP4, WebM, MPEG, WAV, OGG.
* **Data Lake (Iceberg / MinIO / Nessie):** Redpanda tiered storage automatically materializes the `internal.platform.unified.events` and `internal.identity.login.echo` topics into S3 object storage (MinIO) as open-format Apache Iceberg tables. Project Nessie serves as the Iceberg REST Catalog.
* **PyFlink Microservices:** Four Python Flink applications run natively in K8s: `credit_check_processor.py` (atomic credit check + deduction for upload intents), `ttl_expiry_processor.py` (dual-stream `KeyedCoProcessFunction` for upload timeout saga), `move_saga_processor.py` (move-to-permanent saga supervisor with retry + DLQ), and `echo_processor.py` for identity event echo.

---

## 🛠️ Engineering Principles: Strict Best Practices

When extending or maintaining this platform, **strict adherence to framework and language best practices** is mandatory. This ensures longevity, performance, and readability across microservices:

* **Go Architecture Context:** Always utilize type-safe, proven methodologies. Avoid quick scripts. For example, environment configurations must always parse through structural abstraction patterns like `spf13/viper` mapped strictly to defined `Config{}` structs, rather than scattering `os.Getenv` raw calls throughout the codebase. Use standardized libraries (`hamba/avro`, `twmb/franz-go`, `lib/pq` over opaque ORMs).
* **Docker Container Recompilation (Compiled Languages):** When modifying source code for compiled languages (such as Go) that run inside Docker containers, executing `docker compose restart <service>` is **insufficient**. You must strictly execute `docker compose build --no-cache <service>` followed by `docker compose up -d <service>` to flush the cache, recompile the binary natively, and boot the container utilizing the new code.
* **Javascript/Vite Context:** Ensure clear asynchronous fetch boundaries, structured error handling boundaries, native DOM sanitization routines before innerHTML modification, and modular variable scopes. Avoid massive flat `main.js` files where logically splittable.
* **SQL Context:** Write explicit, declarative schema management relying natively on intrinsic Postgres features (RLS, Log Replication realtime engines). Let the database handle access gating optimally over backend application server code.

---

## Directory Structure

* `Taskfile.yml`: Root build system orchestrator — includes domain-local Taskfiles and cross-cutting `taskfiles/*.yml`.
* `taskfiles/`: Cross-cutting Taskfile includes (builders, infra, lint, check, test, helm, lineage, telemetry).
* `.github/workflows/`: GitHub Actions CI (path-filtered builds + GHCR push) and deploy skeleton (staging/production).
* `avro/`: Avro schemas organized by domain (e.g., `avro/public/identity/login.events.avsc`). Directory paths map to Kafka topic names.
* `airflow/`: Airflow DAGs, Helm values (local + prod), and PV/PVC manifests for KubernetesExecutor deployment.
* `charts/event-substrate/`: Helm chart for Go services and Spark SparkApplication CRDs.
* `clickhouse/`: Initialization SQL scripts for the embedded analytical datastore.
* `docker-compose.yml`: Infrastructure definition (Redpanda, Schema Registry, MinIO, ClickHouse).
* `flink_jobs/`: Flink SQL scripts with `${ENV_VAR}` placeholders resolved at runtime by `sql_runner.py`. Kafka source tables are registered centrally; SQL files only define sinks and INSERT logic.
* `tests/e2e/`: End-to-end pipeline tests validating the full data flow into `user_notifications`.
* `docs/github_cicd_plan.md`: Roadmap for GitHub Actions, multisite runners, and the "Lean CI" profile.
* `docs/data_governance_plan.md`: Strategy for Polaris Catalog, Apache Ranger, and PII masking.
* `docs/data_processing_plan.md`: Blueprint for Spark on K8s and Airflow orchestration.
* `frontend/`: Vanilla Vite web application providing the sleek UI for Logins, Signups, and Media Uploads. Includes `media.js` module with upload, credit check, and file management functions.
* `pyflink_jobs/`: Python Flink scripts (`echo_processor.py`, `credit_check_processor.py`, `ttl_expiry_processor.py`, `move_saga_processor.py`).
* `pyspark_apps/`: PySpark batch jobs organized by domain (`identity/`, `media/`, `analytics/`). Each job has its own `app.py`, tests, and fixtures. Common session config in `common/session.py`.
* `go-services/`: Go microservices (`api-gateway`, `message-consumer`, `media-service`).
* `kubernetes/`: Kubernetes deployment manifests for Flink, Spark, Go services, and operators.
* `supabase/`: Database migrations and webhook configurations.
* `telemetry/`: Telemetry configuration files (Prometheus, Grafana, Loki, OpenTelemetry, Marquez).

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
task frontend:dev
```

> [!NOTE]
> **Local Signup Flow**: By default, local Supabase Auth has email confirmations enabled. When you create a new account in the UI, you won't be able to log in immediately. You must either:
>
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

**Go services**

| Command | Description |
|---|---|
| `task go:build` | Build all Go service Docker images (change-detected — only rebuilds if source changed) |
| `task go:build:gateway` | Build API Gateway image only |
| `task go:build:consumer` | Build Message Consumer image only |
| `task go:build:media-service` | Build Media Service image only |

**Flink**

| Command | Description |
|---|---|
| `task flink:build` | Build custom PyFlink image (includes psycopg2) |
| `task flink:configmaps` | Reload Flink SQL/Python ConfigMaps from source (required before redeploying Flink jobs) |
| `task flink:deploy` | Deploy all Flink jobs to Kubernetes |

**Infrastructure & config**

| Command | Description |
|---|---|
| `task infra:operators` | Install Flink Operator and KEDA (one-time) |
| `task infra:auth` | Create Redpanda SASL users and ACLs (idempotent) |
| `task infra:schemas` | Auto-discover and register all Avro schemas from `avro/` |
| `task infra:topics` | Create Kafka topics and enable Iceberg tiered storage |
| `task infra:minio-webhook` | Configure MinIO bucket webhooks for media upload notifications |
| `task infra:clickhouse` | Initialize ClickHouse tables and materialized views |

**Airflow**

| Command | Description |
|---|---|
| `task airflow:install` | Install Airflow on K8s via Helm (creates namespace, PV/PVC, deploys chart) |
| `task airflow:upgrade` | Upgrade Airflow Helm release with current values |
| `task airflow:ui` | Port-forward Airflow API server to `http://localhost:8280` (admin/admin). **Run in a separate terminal** — blocks while active. |
| `task airflow:status` | Show Airflow pod status |
| `task airflow:start` | Scale Airflow deployments back to 1 |
| `task airflow:shutdown` | Scale Airflow deployments to 0 |
| `task airflow:purge` | Uninstall Airflow — Helm release, PV/PVC, namespace |
| `task airflow:logs:scheduler` | Tail Airflow scheduler logs |
| `task airflow:logs:api-server` | Tail Airflow API server logs |

**Helm Chart**

| Command | Description |
|---|---|
| `task helm:template` | Render Helm chart templates (dry-run validation) |
| `task helm:install` | Install/upgrade platform Helm chart (Go services + Spark apps) |
| `task helm:uninstall` | Uninstall platform Helm release |

**Spark**

| Command | Description |
|---|---|
| `task spark:install` | Install Spark Operator on K8s via Helm (one-time) |
| `task spark:build` | Build all Spark images (base + per-app) |
| `task spark:build:base` | Build shared Spark base image (`Dockerfile.spark.base` — runtime + JARs + common/) |
| `task spark:build:identity` | Build identity daily-login-aggregates app image (thin layer on spark-base) |
| `task test:spark` | Run Spark job unit tests (containerized via pyspark-builder) |
| `task spark:test:docker` | Run Spark job unit tests in Docker (exact production base + pytest) |
| `task spark:submit` | Submit SparkApplication to K8s via Helm |
| `task spark:status` | Show SparkApplication status |
| `task spark:logs` | Tail Spark driver logs |
| `task spark:purge` | Uninstall Spark Operator and delete namespace |

**Data Lineage (Marquez)**

| Command | Description |
|---|---|
| `task lineage:start` | Start Marquez lineage stack (API + Web UI + PostgreSQL) |
| `task lineage:ui` | Open Marquez Web UI at `http://localhost:3001` |
| `task lineage:shutdown` | Stop Marquez lineage stack |
| `task lineage:purge` | Stop Marquez and delete all lineage data |

**Batch Layer (Composite)**

| Command | Description |
|---|---|
| `task batch:up` | Start entire batch layer: Spark Operator + Airflow + Marquez lineage stack |
| `task batch:down` | Stop batch layer: scale Airflow to 0, stop Marquez |
| `task batch:purge` | Destroy batch layer: uninstall Airflow + Spark Operator, purge Marquez data |

**Testing**

| Command | Description |
|---|---|
| `task test:e2e` | Run all end-to-end pipeline tests (API → Kafka → Flink → Postgres) |
| `task test:e2e:login` | Test login → user_notifications flow |
| `task test:e2e:message` | Test message POST → user_notifications flow |
| `task test:e2e:upload` | Test media upload saga → credit deduction → notification flow |
| `task test:browser` | Run Playwright browser UI tests — **requires `task frontend:dev` running in a separate terminal first** |

**Telemetry**

| Command | Description |
|---|---|
| `task telemetry:kube-metrics:install` | Install kube-state-metrics for K8s cluster observability (NodePort 30080) |
| `task telemetry:kube-metrics:purge` | Uninstall kube-state-metrics |

**Utilities**

| Command | Description |
|---|---|
| `task frontend:dev` | Install dependencies and start Vite dev server (required before `task test:browser`) |
| `task status` | Show status of all services (Supabase, Docker, K8s pods) |
| `task go:logs:gateway` | Tail API Gateway logs |
| `task go:logs:consumer` | Tail Message Consumer logs |
| `task go:logs:media-service` | Tail Media Service logs |
| `task go:logs:flink` | Tail Flink job logs |
| `task go:health` | Check health of all Go services at once |
| `task go:health:gateway` | Check API Gateway health (`/healthz` + `/readyz`) |
| `task go:health:media-service` | Check Media Service health (`/healthz` + `/readyz`) |
| `task clean` | Clear build checksums (forces full rebuild on next `task start`) |

> [!TIP]
> **Incremental Builds:** Task tracks file checksums in `.task/`. When you modify a Go source file, only the affected service image is rebuilt on the next `task start`. To force a full rebuild, run `task clean` first.

> [!NOTE]
> **Browser Tests:** `task test:browser` runs Playwright against the live Vite dev server. You must have `task frontend:dev` running in a separate terminal before starting the tests. The test suite exercises the full UI flow — login, media upload, credit display, notifications — against the real backend.

## Telemetry & Observability (Grafana + OpenTelemetry + Prometheus)

The platform includes a production-grade observability stack with full instrumentation across Go microservices, Flink processors, and infrastructure layers. The stack is designed to mirror Datadog or Google Cloud Trace/Monitoring without vendor lock-in.

### Telemetry Stack Components

* **OpenTelemetry Collector:** Central hub (OTLP gRPC on 4317, StatsD UDP on 8125) receiving signals from all services
* **Prometheus:** Metrics storage + scraping engine (port 9090) — includes custom Kafka metrics, HTTP server metrics, and infrastructure metrics (Redpanda, MinIO, postgres)
* **Loki:** Log aggregation (port 3100) — consumes structured JSON logs from Go services via zerolog
* **Jaeger:** Distributed tracing (port 16686) — end-to-end request traces with span context propagation
* **Grafana:** Dashboard visualization (port 3000, no login required) — 6 provisioned dashboards with golden signals, alerts, saga health, K8s cluster metrics, and Airflow observability

### Access URLs

When you run `task start`, the telemetry stack boots up alongside the infrastructure:

* **Grafana UI:** [http://localhost:3000](http://localhost:3000)
* **Prometheus UI:** [http://localhost:9090](http://localhost:9090)
* **Jaeger UI:** [http://localhost:16686](http://localhost:16686)
* **Loki:** Port 3100 (queried via Grafana, not directly)
* **Marquez API:** [http://localhost:5050](http://localhost:5050) (OpenLineage event receiver + REST queries)
* **Marquez Web UI:** [http://localhost:3001](http://localhost:3001) (visual lineage graph)

### Grafana Dashboards

Six provisioned JSON dashboards provide full platform visibility:

* **Platform Overview (UID: `platform-overview`)** — service health status, golden signals (latency/errors/throughput), Kafka consumer lag, infrastructure metrics
* **Go Services (UID: `go-services`)** — per-service HTTP request duration/errors, active requests, Kafka producer/consumer message rates and errors
* **Kafka & Redpanda (UID: `kafka-redpanda`)** — cluster health, topic partition distribution, consumer group lag, disk/CPU/network resources
* **Media Saga Pipeline (UID: `media-saga`)** — upload funnel (intents → approvals → signed URLs → confirms), saga DLQ queue depth, Flink processor health
* **Kubernetes Cluster (UID: `kubernetes`)** — pod status/restarts by namespace, deployment & statefulset replica health, CPU/memory resource requests, node conditions
* **Airflow Orchestration (UID: `airflow`)** — scheduler heartbeat, DAG bag size, DAG run duration/states, task instance states, executor/pool slots, task queue depth

### Alert Rules

Twelve alert rules (configurable in `telemetry/grafana/provisioning/alerting/platform-alerts.yaml`):

* **Go service down** — liveness check failed for api-gateway, media-service, or message-consumer
* **Go service high error rate** — >5% of requests returning 5xx errors
* **Go service high latency** — p99 HTTP request duration >5 seconds
* **Media saga DLQ growing** — dead-letter topic lag increasing (indicates move failures)
* **Redpanda disk high** — cluster disk usage >80%
* **Flink job down** — PyFlink credit check, move saga, or TTL expiry processor crashed
* **Postgres connections high** — >70% of available connection slots consumed
* **Kafka consumer lag** — message-consumer group lag >100 messages
* **K8s pod crash-loop** — pod container restarting repeatedly (3m sustained)
* **K8s pod pending** — pod stuck in Pending state for >5 minutes
* **Airflow scheduler down** — scheduler heartbeat absent for >2 minutes (critical)
* **Airflow DAG failure rate** — DAG runs failing in the last 5 minutes

### Instrumentation Details

#### Go Services (api-gateway, media-service, message-consumer)

Each service includes:

* **OpenTelemetry SDK** — initialized in `otel.go` via `initOTel()`, exports traces+metrics to OTel Collector (OTLP gRPC on `OTEL_EXPORTER_OTLP_ENDPOINT`, default `http://host.docker.internal:4317`)
* **Structured Logging** — zerolog JSON logging to stderr (captured by Loki via Docker driver)
* **Health Endpoints** — `/healthz` (liveness, always 200), `/readyz` (readiness, checks Kafka/DB connectivity)
* **HTTP Auto-Instrumentation** — `otelhttp` middleware auto-tracks `http.server.request.duration`, `http.server.active_requests`, `http.server.request.body.size`
* **Custom Kafka Metrics** — `kafka.producer.messages.total`, `kafka.consumer.messages.total`, `kafka.consumer.errors.total` (meter-based, updated on each message)
* **K8s Probes** — liveness probe targets `/healthz:8080`, readiness probe targets `/readyz:8080` (message-consumer also exposes on port 8091)

#### Prometheus Scrape Targets

* **OTel Collector** (port 8888) — converts OTLP signals to Prometheus format
* **Redpanda Admin API** (port 9644, path `/public_metrics`) — cluster health, topic/partition metrics, resource usage
* **MinIO** (port 9000, path `/minio/v2/metrics/cluster`) — enabled via `MINIO_PROMETHEUS_AUTH_TYPE=public` env var
* **postgres-exporter** (port 9187) — connection pool, query latency, cache hit ratios
* **kube-state-metrics** (NodePort 30080) — K8s pod/deployment/statefulset/node metrics via `host.docker.internal`

#### Airflow Telemetry (OTel)

* Airflow 3.x ships with built-in OpenTelemetry support (`apache-airflow[otel]`)
* Metrics and traces push to OTel Collector via OTLP HTTP on port 4318 (same path as Go services use gRPC on 4317)
* Configured in `airflow/values-local.yaml` under `config.metrics` and `config.traces`

#### Flink & PyFlink Instrumentation

* **StatsD Reporter** — Flink native `StatsDReporter` pushes metrics to OTel Collector UDP port 8125
* **Metrics** — Flink SQL/PyFlink jobs emit operator-level throughput, backpressure, and checkpoint timings

### Data Flow Summary

```text
Go Services (OTLP gRPC)        → OTel Collector → Prometheus (metrics)
                                               ↘ Loki (logs via zerolog)
                                               ↘ Jaeger (traces)
                                                   ↓
                                                Grafana (dashboards + alerts)

Airflow pods (OTLP HTTP)       → OTel Collector → Prometheus (metrics) + Jaeger (traces)
Flink/PyFlink (StatsD UDP)     → OTel Collector → Prometheus
kube-state-metrics (NodePort)  → Prometheus scrape
Infrastructure (Redpanda, MinIO, postgres-exporter) → Prometheus scrape
```

### Telemetry Storage Persistence

Telemetry backends use named Docker volumes for local data persistence across `docker compose restart` cycles:

* **Prometheus:** `prometheus_data` volume, 15-day TSDB retention
* **Jaeger:** `jaeger_data` volume, Badger disk-backed span storage (replaces in-memory default)
* **Loki:** `loki_data` volume, persists chunk and index data

> [!NOTE]
> Data survives `docker compose restart` and `docker compose stop/start`, but is destroyed on `docker compose down -v` (i.e., `task purge`). For production, swap to managed backends or S3/GCS — see `docs/productionization.md` Section 8.

### Migrating to Production (Datadog / Google Cloud)

When moving from local development to a cloud provider like Datadog or Google Cloud Operations:

1. **Do not change the application code.** The Go and PyFlink apps remain completely untouched — all instrumentation is vendor-neutral OpenTelemetry.
2. Update the `exporters` block in your production `otel-collector-config.yaml` to use the vendor-specific exporter (e.g., `datadog` for Datadog, `googlecloud` for GCP, `splunk_hec` for Splunk). Provide your API keys and endpoint URLs. The collector will seamlessly route identical signals to your production platform.
