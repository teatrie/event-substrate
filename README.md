# The Event-Driven Substrate

## High-Performance Base Stack for Real-Time Product Engineering

This is a production-grade blueprint for high-scale, event-driven applications. Built on the principles of **Domain-Driven Design (DDD)** and **Event Sourcing**, it provides the underlying "chassis" for rapidly spinning up complex product services—such as high-frequency transactional platforms (Uber) or massive-scale social streams (TikTok).

Instead of building individual features, this stack focuses on the **plumbing of high-throughput data**: strictly authenticated ingress, dynamic routing, real-time stream processing, and sub-second analytical querying.

See [architecture.md](./architecture.md) for a detailed architecture diagram and data flow summary.

---

## Architecture & Tech Stack
*   **Blueprint:** Domain-Driven Design (DDD) & Event Sourcing
*   **Container Runtime:** OrbStack (Optimized for Apple Silicon, running Docker and Kubernetes)
*   **Identity & Read DB:** Supabase CLI (PostgreSQL, GoTrue, Realtime)
*   **Ingress Gateway:** Go custom microservice (`api-gateway`)
*   **Background Consumers:** Go custom microservices (`message-consumer`)
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

### Additional Processing & Storage
*   **Data Lake (Iceberg / MinIO / Nessie):** Redpanda tiered storage automatically materializes the `internal.platform.unified.events` and `internal.identity.login.echo` topics into S3 object storage (MinIO) as open-format Apache Iceberg tables. Project Nessie serves as the Iceberg REST Catalog.
*   **PyFlink Microservice:** A Python Flink application runs natively in K8s, consuming from the `public.identity.login.events` and `public.identity.signup.events` topics and echoing streams structurally to `PyEchoEvent`.

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
- `frontend/`: Vanilla Vite web application providing the sleek UI for Logins and Signups.
- `pyflink_jobs/`: Python Flink scripts (e.g., `echo_processor.py`).
- `go-services/`: Go microservices (`api-gateway`, `message-consumer`).
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

| Command | Description |
|---|---|
| `task init` | First-time full platform setup |
| `task start` | Start platform (incremental Go rebuilds) |
| `task shutdown` | Stop everything |
| `task auth:setup` | Create Redpanda SASL users and ACLs (idempotent) |
| `task build` | Build all Go service images (change-detected) |
| `task build:gateway` | Build API Gateway image only |
| `task build:consumer` | Build Message Consumer image only |
| `task schemas:register` | Auto-discover and register Avro schemas |
| `task topics:create` | Create Kafka topics and enable Iceberg |
| `task clickhouse:init` | Initialize ClickHouse tables |
| `task k8s:operators` | Install Flink Operator and KEDA |
| `task k8s:flink` | Deploy Flink jobs to Kubernetes |
| `task k8s:go-services` | Build and deploy Go microservices to K8s |
| `task test:e2e` | Run all end-to-end pipeline tests |
| `task test:e2e:login` | Test login → user_notifications flow |
| `task test:e2e:message` | Test message → user_notifications flow |
| `task frontend` | Install dependencies and start Vite dev server |
| `task status` | Show status of all services |
| `task logs:gateway` | Tail API Gateway logs |
| `task logs:consumer` | Tail Message Consumer logs |
| `task logs:flink` | Tail Flink job logs |
| `task clean` | Clear build cache (forces full rebuild) |

> [!TIP]
> **Incremental Builds:** Task tracks file checksums in `.task/`. When you modify a Go source file, only the affected service image is rebuilt on the next `task start`. To force a full rebuild, run `task clean` first.

> [!NOTE]
> **Legacy Scripts:** The original `init.sh`, `start.sh`, and `shutdown.sh` bash scripts are retained for reference but `Taskfile.yml` is the primary build system.

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
