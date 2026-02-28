# Project Learnings & Architectural Decisions

This document captures the hard-won wisdom, "gotchas," and strategic decisions made during the development of **The Event-Driven Substrate**.

---

## 🛠️ Infrastructure & Local Development

### 1. High-Performance Local Mirroring

**Observation:** Running a full production-grade stack (Redpanda, Flink, ClickHouse, OTel, Spark) on a local machine requires significant resource efficiency.
**Decision:** Standardized on **OrbStack** for Apple Silicon. Its native virtualization layer handle the Redpanda tiered storage (S3/MinIO) and Kubernetes management with significantly lower overhead than traditional local Docker Desktop solutions.

### 2. Helm Adoption: Migrating from kubectl to Helm

**Observation:** When Go services were originally deployed via `kubectl apply -f kubernetes/go-services/`, the resulting K8s resources lack Helm ownership metadata. Switching to `helm upgrade --install` fails with `invalid ownership metadata; label validation error: missing key "app.kubernetes.io/managed-by"` because Helm refuses to adopt resources it didn't create.
**Decision:** Added an idempotent resource adoption step in the `helm:install` task that annotates and labels existing resources with `meta.helm.sh/release-name`, `meta.helm.sh/release-namespace`, and `app.kubernetes.io/managed-by=Helm` before running `helm upgrade --install`. This is a one-time migration — once Helm owns the resources, subsequent installs work cleanly.

### 3. Spark Docker Split: Base + Per-App Layers

**Observation:** A monolithic Spark Dockerfile that bundles the runtime, JARs, common code, and all app code forces a full rebuild whenever any single app changes. As the number of Spark jobs grows, this becomes wasteful.
**Decision:** Split into `Dockerfile.spark.base` (Spark 4.0.2 + JARs + pip deps + `common/`) and per-app thin Dockerfiles (e.g., `pyspark_apps/identity/daily_login_aggregates/Dockerfile`) that `FROM spark-base:latest`. Base image only rebuilds when JARs, common code, or dependencies change. Per-app images rebuild in seconds since they only COPY the app code.

### 4. Airflow 3.x Scheduler OOMKilled at Default Memory Limits

**Observation:** Airflow 3.x's scheduler consumes significantly more memory than 2.x. With a 512Mi memory limit, the scheduler is OOMKilled during startup — especially on a loaded system running Flink, Redpanda, Spark, and OTel concurrently. The default Helm startup probe (20s timeout) also times out before the scheduler can initialize, compounding the issue into a crash loop.
**Decision:** Bumped scheduler memory limit from 512Mi to 1Gi and CPU limit from 300m to 500m in `airflow/values-local.yaml`. Added an explicit startup probe with 30s timeout and 30 retries (300s total window). The triggerer can be slow to start under load but doesn't affect DAG execution — only deferrable operators need it.

### 5. Containerized Builder Images for Local ↔ CI Parity

**Observation:** Native host builds pollute the developer's environment with multiple Go, Python, and Node.js versions plus 11+ CLI tools. CI reinstalls everything from scratch on every run (slow, fragile, version drift risk). Different linting results between local and CI cause confusion.
**Decision:** Created 5 Docker builder images (`docker/builders/`) containing pinned tool versions: go-builder (Go 1.26 + golangci-lint v2.10.1), pyflink-builder (Python 3.10 + ruff + mypy), pyspark-builder (Python 3.12 + ruff + mypy + pyspark + pytest), frontend-builder (Node.js 22), infra-lint (yamllint, sqlfluff, kubeconform, markdownlint-cli2, maid, helm). All `task lint:*`, `check:*`, `test:go`, and `spark:test` run inside containers via `docker run`. Named volumes (`event-substrate-gomod`, `event-substrate-frontend-nm`) cache Go modules and `node_modules` between runs. CI uses the same images via `container:` directives.
**Gotchas:**

- **arm64/amd64:** The infra-lint image uses `ARG TARGETARCH` for kubeconform and Helm binary downloads. Docker automatically sets this based on build platform. CI builds `linux/amd64` only.
- **Named volumes:** `docker volume rm event-substrate-gomod` if Go module cache becomes corrupted. Same for `event-substrate-frontend-nm`.
- **Bootstrap:** Builder images must exist in GHCR before CI jobs can use them. Run the `builders.yml` workflow manually (`workflow_dispatch`) on first setup, or after any Dockerfile change to `docker/builders/`.
- **Source mounting:** Lint tasks mount `:ro` (read-only) to prevent tools from modifying source. `lint:fix` mounts writable.
- **Read-only mount cache failures:** Many tools (ruff, mypy, ESLint) write caches to the working directory by default. With `:ro` mounts this causes `Read-only file system` errors. Fix per tool: ruff needs `--no-cache`, mypy needs `--cache-dir /tmp/.mypy_cache`, ESLint/Node tools work because `node_modules` is a separate writable named volume. Any new containerized tool should be tested with `:ro` mounts and may need a cache redirect flag.
- **Task `sources:` change detection:** Builder images only rebuild when their Dockerfile changes — `task builders:go` is a no-op if `Dockerfile.go-builder` hasn't changed.

### 6. Taskfile Split: Domain-Local + Cross-Cutting Includes

**Observation:** The root `Taskfile.yml` grew to 1062 lines. Adding a new service or Spark app meant editing this monolith, increasing merge conflict risk and cognitive load.
**Decision:** Split into 13 included Taskfiles using Task v3.48.0's `includes:` feature. Domain-local files live next to their code (`go-services/Taskfile.yml`, `flink_jobs/Taskfile.yml`, `pyspark_apps/Taskfile.yml`, `frontend/Taskfile.yml`, `airflow/Taskfile.yml`). Cross-cutting files live in `taskfiles/` (`builders.yml`, `infra.yml`, `helm.yml`, `lint.yml`, `check.yml`, `test.yml`, `lineage.yml`, `telemetry.yml`). Root Taskfile is now ~180 lines: vars, includes, composites (`init`, `start`, `shutdown`, `purge`, `batch:*`), aliases, and utility tasks.
**Gotchas:**

- **Cross-namespace deps:** Included Taskfiles resolve `deps:` within their own namespace. `deps: [builders:go]` in `lint.yml` (included as `lint:`) resolves to `lint:builders:go`, not root `builders:go`. Fix: add a nested `includes: { builders: { taskfile: ./builders.yml, dir: .. } }` in each cross-cutting file that needs builder deps (`lint.yml`, `check.yml`, `test.yml`). The nested builder tasks are `internal: true`, so they don't pollute `task --list`.
- **Root tasks unreachable from includes:** Root-level tasks (`init`, `purge`, `clean`) cannot be called from included files. Tasks like `test:cold-start` that call root tasks must stay in the root Taskfile.
- **`dir` in nested includes:** When an included file (`taskfiles/lint.yml`) includes a sibling file (`taskfiles/builders.yml`), `dir: ..` is relative to the taskfile's directory (`taskfiles/`), pointing correctly to the project root.
- **yamllint + flow-style YAML:** Aligned flow-style includes (`{ taskfile: ..., dir: . }`) violate yamllint's `braces` and `colons` rules. Use block-style YAML for includes.
- **`dir` defaults to entrypoint, not included file:** When an included Taskfile omits `dir:`, Task defaults to the **entrypoint** Taskfile's directory (project root), not the included file's directory. `frontend/Taskfile.yml` running `npm run dev` failed with `ENOENT: package.json` because it ran from the project root. Fix: always set `dir:` explicitly on includes whose tasks expect to run from their own directory (e.g., `frontend: { taskfile: ./frontend/Taskfile.yml, dir: frontend }`).

---

## 🏗️ Architecture & Data Flow

### 1. Real-time Race Conditions (Flink vs. WebSockets)

**Observation:** Flink processes data at sub-millisecond speeds. In a local environment, an event can often traverse the entire pipeline and be inserted back into the database *before* a client's frontend (Vite/Supabase-js) has successfully established its WebSocket subscription following a login.
**Decision:** Implemented a **REST-prefetch fallback** strategy. The client concurrently fetches historical state while connecting to the stream to ensure no events are missed during the "socket negotiation" window.

## 🔄 Pipeline Stabilization & Realtime Data

### 1. Supabase REPLICA IDENTITY FULL

**Observation:** Supabase Realtime streams Postgres changes over WebSockets, but for UPDATEs and DELETEs, it often drops the full row payload (sending only the primary key).
**Decision:** We learned to explicitly configure `ALTER TABLE ... REPLICA IDENTITY FULL;` in our SQL migrations. Without this, downstream edge clients utilizing the Realtime subscriptions fail to retrieve the complete data map when records are modified.

### 2. Unified Notification Channel

**Observation:** We encountered a "phantom data loss" bug where backend Flink pipelines were perfectly materializing views back into Postgres, but the UI wasn't updating. The root cause was the frontend subscribing to individual domain tables that were out of sync with the backend.
**Decision:** Consolidated all event egress into a single `user_notifications` table. The frontend subscribes to one Supabase Realtime channel on this table, parsing `event_type` and `payload` to render notifications. This eliminates the need to add new subscriptions when new event types are introduced.

---

## 🌊 Streaming Reliability & Schemas

### 1. Aggressive Consumer Crashing (Data Loss Prevention)

**Observation:** Inside Go Kafka consumers, if a downstream execution (like a Postgres `INSERT`) fails, handling the error gracefully and `return`ing out of the function allows the Kafka offset to advance. This functionally creates silent, permanent message loss across the pipeline.
**Decision:** We adopted an aggressive failure philosophy. We now use `log.Fatalf()` to deliberately crash the individual consumer pod if an Egress operation fails. This prevents the offset from committing, ensuring the message is retried indefinitely upon Kubernetes pod respawn (acting as a strict Dead-Letter queue forcing manual intervention rather than data loss).

### 2. Avro Schema Registration Contexts

**Observation:** When iteratively renaming Kafka topics (e.g., `LoginEvent` to `public.identity.login.events`), simply updating producer/consumer config strings and cluster topics is insufficient.
**Decision:** You must explicitly `POST` and re-register the `.avsc` schema files under the exact new topic namespace (`<new_topic_name>-value`) to the Confluent Schema Registry. Flink consumer mapping pods will otherwise suffer 'Subject not found' timeouts and completely break pipeline mappings until both the registry and the pods are cleared.

### 3. Iceberg Catalog Requires Hadoop on Classpath

**Observation:** Registering an Iceberg REST catalog (`CREATE CATALOG iceberg_catalog WITH ('type' = 'iceberg', ...)`) in Flink 1.18 fails with `ClassNotFoundException: org.apache.hadoop.conf.Configuration` even when `iceberg-flink-runtime-1.18-1.5.0.jar` is on the classpath.
**Decision:** The Iceberg Flink runtime depends on `hadoop-common` for configuration handling. Since the core notification pipeline doesn't need Iceberg (it's for querying tiered storage), the catalog registration is wrapped in a try/except and treated as non-fatal. To fully enable it, add `hadoop-common-3.3.6.jar` to the Flink `usrlib/` directory.

### 4. Deleting Supabase Migrations Requires DB Reset

**Observation:** When migration files that were already applied are deleted from `supabase/migrations/`, the migration history in the database becomes out of sync. Subsequent migrations may not apply, and tables created by deleted migrations persist as orphans.
**Decision:** After deleting applied migration files, always run `supabase db reset` to re-apply the remaining migrations from scratch. This ensures the database schema matches the migration directory exactly.

---

## 🐳 DevOps & Environment Recovery

### 1. Idempotent Startup Scripts

**Observation:** Because our local Docker volumes (ClickHouse MVs, Kafka Engine tables) are occasionally pruned or subject to ephemeral desyncs during development.
**Decision:** Initialization actions (like `clickhouse-client -n < clickhouse/init.sql`) must be baked into the everyday `./start.sh` boot script, rather than isolating them to a one-time `./init.sh` setup script. This guarantees the entire environment Recovers and re-asserts its state completely if containers are recreated.

### 2. Task `sources:` Is for Build Artifacts, Not Infrastructure State

**Observation:** Task's `sources:` change-detection is designed for build artifacts ("recompile if source changed"). Using it on infrastructure state tasks (schema registration, topic creation) causes silent skips when containers are rebuilt but config files haven't changed — the Schema Registry is empty but Task reports "up to date."
**Decision:** Removed `sources:` from `schemas:register` and `topics:create`. These are idempotent reconciliation tasks (~2s each) that must always run to assert external state. Build tasks (`build:gateway`, `build:consumer`) still use `sources:` because they track local artifacts, not external system state. The `start` task now includes both schema and topic reconciliation.

### 3. Dockerfile Build Context Must Track All Source Files

**Observation:** Hardcoding individual filenames in Dockerfiles (e.g., `COPY main.go ./`) causes build failures when new source files are added. The error only surfaces at Docker build time — local builds succeed because the compiler sees all files in the directory.
**Decision:** Use glob patterns (`COPY *.go ./`, `COPY src/ ./src/`) in Dockerfiles. When adding any new source file to a service, verify its Dockerfile copies it into the build context.

### 4. `supabase stop` Preserves Auth State — Use `--no-backup` for Full Purge

**Observation:** `task purge` ran `supabase db reset` after `supabase stop`, which failed silently (`|| true`). Even when the public schema was wiped, `auth.users` survived because GoTrue manages auth state independently of the public schema. The browser's JWT remained valid against the surviving auth user, causing stale sessions to persist across purge cycles.

**Decision:** Use `supabase stop --no-backup` to delete all data volumes (including GoTrue's auth state). A stale JWT against a wiped DB should also be caught client-side via `getUser()` validation — if the server returns an error, auto-sign out. The `task purge` sequence is: `supabase stop --no-backup` → `supabase start` → `supabase db reset`.

---

## 🔐 Kafka Authentication (SASL/SCRAM)

### 1. Redpanda `enable_sasl` Is a Cluster Property, Not a Node Property

**Observation:** Setting `--set redpanda.enable_sasl=true` in the `rpk redpanda start` command flags writes to the node config (`redpanda.yaml`), but SASL enforcement is a *cluster* property. The cluster config remains `enable_sasl=false` unless set via the bootstrap file or Admin API.
**Decision:** Set `enable_sasl: true` and `superusers` in `redpanda-bootstrap.yaml` (applied at cluster formation). This ensures SASL is enforced from first boot. If changing after cluster creation, use Admin API: `rpk cluster config set enable_sasl true`.

### 2. Schema Registry Needs Topic Create + Alter ACLs

**Observation:** Schema Registry creates the `_schemas` topic on first startup. The initial ACL set (Read/Write/Describe) was insufficient — it also needs `Create` and `Alter` permissions on `_schemas` to bootstrap the topic with the correct configuration.
**Decision:** Grant `Create` and `Alter` alongside Read/Write/Describe for the `schema-admin` identity on the `_schemas` topic.

### 3. SCRAM Credentials Need Propagation Time

**Observation:** After creating SASL users via the Admin API, the credentials are not immediately available for Kafka protocol authentication. Attempting ACL creation (which authenticates as `superuser` via Kafka protocol) immediately after user creation fails with `ILLEGAL_SASL_STATE`.
**Decision:** Added a retry loop (`rpk cluster info --user superuser ...`) between user creation and ACL setup to wait for credential propagation.

### 4. Flink Kafka Connector Uses Shaded Class Names

**Observation:** The Flink SQL Kafka connector (`flink-sql-connector-kafka-3.0.1-1.18.jar`) shades all Kafka client classes under `org.apache.flink.kafka.shaded.*`. Using the original `org.apache.kafka.common.security.scram.ScramLoginModule` in JAAS config fails with `No LoginModule found`.
**Decision:** Use the shaded path: `org.apache.flink.kafka.shaded.org.apache.kafka.common.security.scram.ScramLoginModule`.

### 5. JAAS Semicolons Conflict with SQL Statement Splitting

**Observation:** `sql_runner.py` splits SQL files on `;` to execute individual statements. JAAS config strings contain `;` (e.g., `...password="pw";`) which gets mis-split, breaking CREATE TABLE statements.
**Decision:** Moved all Kafka table registrations (sources AND sinks) into `sql_runner.py` Python code. SQL files now only contain INSERT logic. This avoids the `;` conflict entirely.

### 6. ClickHouse Kafka SASL Uses Server Config, Not Table Settings

**Observation:** ClickHouse 24.3's Kafka engine does not support `kafka_security_protocol` or `kafka_sasl_*` settings inline in `CREATE TABLE ... SETTINGS`. Attempting this fails with `Unknown setting`.
**Decision:** SASL config goes in a server config XML file (`clickhouse/kafka-sasl.xml`) mounted at `/etc/clickhouse-server/config.d/`, which applies to all Kafka engine tables globally.

### 7. Viper `AutomaticEnv()` Requires Key Registration

**Observation:** `viper.AutomaticEnv()` only binds env vars for keys that viper already knows about (via `SetDefault`, `BindEnv`, or config files). New Config struct fields with `mapstructure` tags but no corresponding `SetDefault()` silently remain empty.
**Decision:** Always add `viper.SetDefault("KEY", "")` for every Config struct field, even if the default is empty string.

### 8. RLS Must Scope Message Visibility — Not Just Event Type

**Observation:** The original RLS policy `USING (auth.uid()::text = user_id OR event_type = 'user.message')` made ALL messages visible to ALL authenticated users. This caused E2E test messages (e.g., `e2e-test-*` from `test-*@e2e-test.local` users) to leak into real users' Live Events feeds.
**Decision:** Added `visibility` (`broadcast` | `direct`) and `recipient_id` columns to `user_notifications`. The RLS policy now has three clauses: own events, broadcast messages, or direct messages where `recipient_id = auth.uid()`. E2E tests send with `visibility: 'direct'` targeting the test user, so they never pollute the broadcast feed. The Avro schema uses a default of `"broadcast"` for backward compatibility.

---

## 📁 Media Upload & Credit Economy

### 1. MinIO Webhook Event Format Requires Transformation

**Observation:** MinIO S3 event notifications use a nested JSON structure (`Records[].s3.bucket.name`, `Records[].s3.object.key`, etc.) that doesn't directly map to our Avro schemas. The event also includes URL-encoded keys and metadata in custom headers (`x-amz-meta-*`).
**Decision:** Built dedicated webhook handlers in the media-service (`upload_webhook_handler.go`, `file_ready_webhook_handler.go`) that extract and transform the MinIO event format into flat Avro-compatible structures before producing to Kafka. This keeps downstream processors simple — they read clean, pre-normalized events.

### 2. Append-Only Credit Ledger Over Mutable Balance Column

**Observation:** A mutable `balance` column on a `user_credits` table would require UPDATE operations, which conflict with Flink JDBC sinks (append-only, no PRIMARY KEY for pure INSERT). It also loses the audit trail of individual credit transactions.
**Decision:** Used an append-only `credit_ledger` table where each row is a signed delta (`+2` for signup, `-1` for upload). A `user_credit_balances` view aggregates `SUM(amount)` grouped by `user_id`. This fits Flink's INSERT-only model, provides a full audit trail, and enables future credit analytics.

### 3. Credit Deduction at Intent Time with TTL Refund (Upload Saga)

**Observation:** The original design deducted credits on upload completion (MinIO webhook). This left a race window and didn't charge for abandoned uploads. The Upload Saga inverts this: credits are deducted atomically at intent time via PyFlink's conditional SQL (`INSERT ... WHERE balance >= 1`).
**Decision:** Deduct at intent, refund on expiry. The `credit_check_processor.py` atomically checks and deducts in a single SQL query (no race window). If the upload doesn't complete within 15 minutes, the `ttl_expiry_processor.py` emits `FileUploadExpired`, and `credit_balance_processor.sql` refunds +1 credit. This ensures users are never permanently charged for failed uploads while eliminating the multi-URL race condition.

### 3b. TTL Processor Keys by file_path, Not request_id

**Observation:** The `upload.signed` event has `request_id` but `upload.received` (MinIO webhook) does not — there's no way to correlate them by request. Both events share `file_path`.
**Decision:** The TTL processor keys its dual-stream `KeyedCoProcessFunction` by `file_path`. This correctly correlates signed URLs with their upload receipts. Trade-off: if the same file_path is re-signed (e.g., on retry), the second signed event overwrites state and resets the timer.

### 3c. FileUploadExpired Schema Gap: file_size and media_type

**Observation:** The `FileUploadExpired` Avro schema requires `file_size` and `media_type`, but `upload.signed` (the TTL processor's primary input) doesn't carry them — they live on `upload.approved`.
**Decision:** Default to `0` and `""` for now. Downstream consumers (credit refund, expired cleanup, notifications) don't depend on these fields for correctness. A future enhancement could add `upload.approved` as a third input stream to the TTL processor.

### 3d. Remove Old Deduction Paths When Adding a Saga

**Observation:** After implementing the Upload Saga (credit deduction at intent time in `credit_check_processor.py`), the old Flink SQL deduction in `credit_balance_processor.sql` was left in place. Both paths converge on `public.media.upload.events` when the file lands in MinIO, causing a double deduction: -1 at intent approval AND -1 at file arrival. The Flink SQL processor has no way to distinguish saga uploads from legacy uploads.
**Decision:** When moving a side effect (like credit deduction) from a downstream processor to an upstream saga, always remove the downstream logic completely. Credit deduction must happen at exactly one point — the authorization boundary. The `credit_balance_processor.sql` now only creates `media_files` rows and handles expired-upload refunds; all upload credit logic lives in the saga.

### 4. CORS Required for Browser-Direct Presigned URL Uploads

**Observation:** When the browser uploads directly to MinIO/GCS via a presigned PUT URL, the request is cross-origin (different port/domain than the frontend). Without CORS headers on the storage bucket, the browser blocks the upload with an opaque network error.
**Decision:** Configured CORS on the MinIO `media-uploads` bucket via `scripts/minio-init.sh` (`mc anonymous set-json`). In production, the GCS bucket CORS config must restrict `AllowedOrigins` to the production frontend domain(s) rather than `*`.

### 5. Presigned URLs Decouple Upload Size from API Gateway Memory

**Observation:** Proxying file uploads through the API Gateway would require buffering the entire file in memory (or streaming with backpressure), increasing memory footprint and latency linearly with file size.
**Decision:** The Claim Check pattern sends only metadata through the event pipeline. The browser uploads directly to object storage via presigned PUT URLs. The API Gateway never sees the file bytes — it only validates auth, checks credits, and signs the URL. This keeps the Gateway stateless and lightweight regardless of upload size.

### 6. S3v4 Presigned URLs Cannot Have Their Host Rewritten

**Observation:** S3v4 presigned URLs include the `Host` header in the canonical request that's hashed into the signature. If you rewrite `host.docker.internal:9000` → `localhost:9000` in the URL, the browser sends `Host: localhost:9000`, which doesn't match the signed host, and MinIO returns `SignatureDoesNotMatch` (403).
**Decision:** Never rewrite the host portion of a presigned URL. Instead, ensure the client can resolve the original hostname. In E2E tests, use Node's `http.request` API (not `fetch`) to connect to `127.0.0.1` while explicitly sending the original `Host: host.docker.internal:9000` header. Node's `fetch` (undici) silently strips custom `Host` headers for security — only `http.request` preserves them.

### 7. MinIO Sends URL-Encoded Object Keys in Webhook Events

**Observation:** MinIO S3 event notifications URL-encode the object key — forward slashes become `%2F` (e.g., `uploads%2F{user_id}%2F{uuid}%2Fimage.png`). Splitting on `/` without decoding first yields a single segment, causing path parsing to skip the record as non-matching.
**Decision:** Always `url.QueryUnescape` the `Records[].s3.object.key` field before parsing the path structure. This is not documented in MinIO's webhook documentation and only surfaces when keys contain path separators.

### 8. JSON Number Type Coercion Breaks Avro `long` Fields — Full Fix Chain

**Observation:** Go's `encoding/json` package has three levels of numeric handling, each insufficient on its own for Avro:

1. `json.Unmarshal` into `map[string]any` → all numbers become `float64` → Avro rejects with "float64 is unsupported for Avro long"
2. `json.NewDecoder().UseNumber()` into `map[string]any` → numbers become `json.Number` (string type) → Avro rejects with "json.Number is unsupported for Avro long"
3. Both are needed: `UseNumber()` preserves the exact representation, then a custom `convertJSONNumbers()` walker converts `json.Number` → `int64` (via `.Int64()`) or `float64` (via `.Float64()`) for native Go types that hamba/avro accepts.

**Decision:** In `processAndProduceEvent` (the shared entry point for ALL event production), use `json.NewDecoder().UseNumber()` for deserialization, then call `convertJSONNumbers(event)` to recursively convert `json.Number` values to native `int64`/`float64` before Avro encoding. This fix benefits all topics with numeric Avro fields, not just media uploads.

### 9. MinIO Webhook Auth Requires Version-Specific Configuration

**Observation:** MinIO's `mc admin config set notify_webhook:<name> auth_token=...` parameter exists in documentation but is not supported in all MinIO versions. The config silently ignores the `auth_token` field, causing webhooks to arrive without any authentication header.
**Decision:** Do not rely on `auth_token` for MinIO webhook authentication in local dev. Instead, authenticate MinIO's webhook endpoint by network isolation (the endpoint is only reachable from within the Docker/K8s network). Add a `TODO(prod)` for IP allowlisting or mutual TLS in production. Always check `mc admin config get` output to verify which fields are actually persisted.

### 10. MinIO Webhook Setup Is Infrastructure State, Not Build State

**Observation:** MinIO's webhook notification configuration (target endpoint, event subscriptions) is stored in MinIO's internal state, not in config files. Restarting the MinIO container preserves the config, but re-creating the container (via `docker compose down`/`up` or volume pruning) loses it. Unlike Redpanda topics/schemas, there's no `sources:` fingerprinting — the webhook must be re-applied.
**Decision:** The `scripts/setup-minio-webhook.sh` script must be included in the `task start` startup sequence, similar to `schemas:register` and `topics:create`. It is idempotent (re-applies are safe) and should always run to reconcile state. The `AGENT.md` deployment checklist calls this out explicitly.

### 11. MinIO Event Queue Persists Failed Deliveries Across Restarts

**Observation:** When a MinIO webhook returns a non-2xx status, MinIO enqueues the event for retry (every ~3 seconds). This queue persists across container restarts and even survives deletion of the original object. A single failed webhook can generate thousands of retry attempts, dominating service logs and potentially blocking new event deliveries via queue saturation.
**Decision:** When debugging MinIO webhook issues: (1) always check media-service logs for the actual user ID — if you see the SAME user retrying, it's a stale event, not a new upload; (2) restart MinIO AND re-run `setup-minio-webhook.sh` to clear the retry queue; (3) clear stale objects with `mc rm --recursive --force` before re-testing. The webhook handlers in media-service return proper HTTP status codes to MinIO, so successful events get acknowledged immediately (200) while failures get retried (400+).

### 12. Flink `NoRestartBackoffTimeStrategy` Turns Transient Failures Into Silent Permanent Death

**Observation:** When Flink's JDBC sink fails to reconnect to Postgres (e.g., due to connection slot exhaustion), the default `NoRestartBackoffTimeStrategy` prevents the job from restarting. The FlinkDeployment still reports `JOB STATUS: RUNNING / LIFECYCLE STATE: STABLE` in `kubectl get flinkdeployments`, masking the crash. Events pile up in Kafka with no consumer.

**Decision:** Set `restart-strategy: exponential-delay` in `flinkConfiguration` with a 5s initial backoff, 5min max, and 10 max attempts. This recovers automatically from transient failures (JDBC timeouts, momentary connection exhaustion) without operator intervention. The retry count cap prevents infinite loops on permanent failures.

### 13. Flink `avro-confluent` Format Auto-Generates Schema Names

**Observation:** When Flink's `avro-confluent` Kafka sink format serializes records, it auto-generates an Avro schema name from the SQL table definition (e.g., `upload_rejected_sink` or `record`). If a schema is already registered under the same subject with a different record name (e.g., `public.media.FileUploadRejected`), the Schema Registry's BACKWARD compatibility check catches the name change and returns a 409 `NAME_MISMATCH` error. The Flink job then enters a crash loop.

**Decision:** For subjects where Flink is the sole producer via `avro-confluent` format, set compatibility to `NONE` in the Schema Registry. This allows Flink's auto-generated schema to coexist with the canonical Avro schema. The affected subjects (`public.media.upload.approved-value`, `public.media.upload.rejected-value`, `public.media.download.rejected-value`) are set to NONE in the `schemas:register` task. An alternative (not pursued) would be to serialize Avro records manually with explicit schema names, but that eliminates the convenience of Flink SQL sinks.

### 14. E2E Tests Need a Flink Warmup Gate After Fresh Deploy

**Observation:** After `task purge && task init`, the FlinkDeployment CRD reports `RUNNING / STABLE` within seconds, but the taskmanager takes ~2 minutes to fully initialize (compile SQL, connect to Kafka, start consuming). E2E tests that run immediately after init hit a 30-second timeout because Flink isn't processing events yet. The notification eventually appears in the database — just 2+ minutes late.

**Decision:** Added a `warmup.mjs` canary test to `run_all.sh` that exercises the full pipeline with a 3-minute timeout: (1) signs up a throwaway user and waits for `identity.signup` notification (warms identity + credit Flink jobs), then (2) performs a complete canary upload — credit wait, sign in, upload-intent, presigned URL, MinIO PUT, wait for `media_files` row (warms credit check PyFlink, move saga PyFlink, and SQL runner Flink SQL). If the canary succeeds, all Flink jobs have completed at least one checkpoint cycle and subsequent tests use standard 30s timeouts. If it fails, the pipeline is genuinely broken — not just slow. **Key insight:** canary signup alone is insufficient after a fresh deploy — the upload pipeline (PyFlink credit check + move saga) takes additional checkpoint cycles beyond what the identity pipeline exercises.

### 15. Postgres Connection Exhaustion Kills Flink Silently

**Observation:** Local Supabase has a limited connection pool. Long-running Flink JDBC sink sessions can exhaust it, causing `FATAL: remaining connection slots are reserved for roles with the SUPERUSER attribute`. Once this occurs, Flink's JDBC reconnect fails, triggering the restart strategy (or killing the job permanently if no strategy is set). The symptom is uploads succeeding (MinIO → Kafka event produced) but files never appearing in My Files and no Live Events notification.

**Decision:** Add a `postgres-exporter` sidecar + Prometheus scrape + Grafana alert at 70+ active connections to catch this before it kills Flink. When it does occur: run `supabase db reset` to free connections, then restart the FlinkDeployment. Always diagnose silent upload failures by checking `kubectl logs` on the Flink pod — if the job crashed hours ago, events are sitting in Kafka waiting for replay on restart.

---

## 📂 Move-to-Permanent Storage Saga

### 16. MinIO Webhook Targets Must Be Environment Variables, Not Runtime Config

**Observation:** MinIO webhook notification *targets* (the endpoint URL and enable flag) must be defined via environment variables in `docker-compose.yml` (`MINIO_NOTIFY_WEBHOOK_ENABLE_<NAME>`, `MINIO_NOTIFY_WEBHOOK_ENDPOINT_<NAME>`). Attempting to create targets at runtime via `mc admin config set notify_webhook:<name> endpoint=...` silently fails — the config appears to set but MinIO doesn't register the target. Subsequent `mc event add` calls then fail with "A specified destination ARN does not exist or is not well-formed" because the ARN references a target that was never actually created.

**Decision:** Define webhook targets exclusively as MinIO env vars in `docker-compose.yml`. The setup script (`setup-minio-webhook.sh`) should only add *bucket event notifications* (`mc event add` with ARN, event type, and prefix filter) — never attempt to create the targets themselves. Changing webhook targets requires recreating the MinIO container (`docker compose down` + `up`, not just `restart`) to pick up the new env vars.

### 17. Prefix-Scoped Webhooks Enable Domain-Specific Event Routing

**Observation:** A single MinIO bucket can have multiple named webhook targets, each scoped to a different key prefix via `mc event add --prefix`. This avoids needing a single webhook handler to demux events by path — each handler only receives events for its prefix.

**Decision:** The `media-uploads` bucket uses two webhook targets: `MEDIASERVICE_UPLOADS` (prefix `uploads/` → `/webhooks/media-upload`) and `MEDIASERVICE_FILES` (prefix `files/` → `/webhooks/file-ready`). Each target has its own ARN (`arn:minio:sqs::<NAME>:webhook`), and each handler receives only the events it cares about. When adding new object lifecycle stages, add a new named webhook target + handler rather than overloading existing ones.

### 18. Idempotent MoveObject: Check Destination Before Failing on Missing Source

**Observation:** In a saga retry loop, `CopyObject` may fail with `NoSuchKey` if the source was already removed by a previous successful move attempt. Treating this as a hard error causes the retry to fail even though the move already completed.

**Decision:** The `minioObjectMover` adapter handles `NoSuchKey` on `CopyObject` by checking if the destination already exists via `StatObject`. If the destination is present, the move already succeeded — return nil. This makes MoveObject safe for retries without requiring external state tracking. The pattern: `CopyObject → (NoSuchKey? → StatObject dest → exists? → nil) → RemoveObject src`.

### 19. Saga Retry: Async First Attempt, Synchronous Retry

**Observation:** The webhook handler fires MoveObject in a goroutine (fire-and-forget) and returns 200 immediately after producing the `upload.received` Kafka event. This is correct because the saga supervisor (Flink) will detect if the move failed and trigger a retry. However, the retry handler must call MoveObject *synchronously* — if it fired-and-forgot, it would re-produce `upload.received` before knowing if the move succeeded, potentially causing an infinite retry loop where the move never completes but keeps re-entering the saga.

**Decision:** First attempt (webhook): async MoveObject — speed matters, saga is the safety net. Retries (retry handler): synchronous MoveObject — correctness matters, the retry IS the safety net. The retry handler only re-produces `upload.received` (re-entering the saga loop) after confirming MoveObject returned nil.

### 20. TTL Expiry Must Key on Upload Receipt, Not Saga Completion

**Observation:** The TTL processor detects "user got a presigned URL but never uploaded." The completion signal should be `upload.received` (file hit staging), not `upload.confirmed` (saga completed and file moved to permanent). If the move saga fails but the user DID upload the file, their credit should NOT be refunded — the file exists in staging, the saga will retry, and the upload will eventually complete.

**Decision:** Changed TTL processor stream 2 from `public.media.upload.events` to `internal.media.upload.received`. The TTL timer cancels as soon as the file lands in staging, regardless of whether the move-to-permanent saga has completed. Credit refunds only happen when the user genuinely didn't upload within the TTL window.

### 21. Saga Supervisors Need Three-Outcome Branching, Not Two

**Observation:** A naive two-stream join (both present → success, missing → retry) creates an infinite retry loop. Without a retry count cap, transient failures (e.g., MinIO down) cause permanent event storms as the saga continuously retries.

**Decision:** Every saga supervisor must have three outcomes: (1) success — both events present, (2) retry — timeout + retry_count < MAX, (3) dead letter — timeout + retry_count >= MAX. The `retry_count` is carried in the event payload and incremented by the retry handler, not the saga supervisor. The DLQ event auto-tiers to Iceberg via Redpanda's `redpanda.iceberg.mode` for post-mortem analysis, and triggers a user-facing `media.upload_failed` notification so the user knows to retry manually.

### 22. PyFlink on_timer Context Does Not Support Side Outputs

**Observation:** PyFlink 1.18's `KeyedCoProcessFunction.on_timer` receives an `InternalKeyedProcessFunctionOnTimerContext` that lacks the `.output()` method for side outputs. Calling `ctx.output(tag, row)` in `on_timer` raises `AttributeError`. This is a PyFlink-specific limitation — the Java API supports side outputs from all context types.

**Decision:** Avoid side outputs entirely in PyFlink DataStream jobs. Instead, use a single wide output Row with a `_type` discriminator field. All event types (confirmed, retry, dead-letter) flow through the main output. Downstream SQL `WHERE _type = 'confirmed'` / `'retry'` / `'dead_letter'` routes events to the correct Kafka sinks. This is simpler, avoids the API gap, and reduces the number of distinct Row types to maintain.

### 23. Fast-Path Saga Confirmation Avoids Unnecessary Timer Waits

**Observation:** The saga supervisor originally only emitted results from `on_timer` (120s after `upload.received`). Even when both events arrived within milliseconds, the pipeline waited the full timeout before producing `upload.confirmed`. This made E2E tests (60s timeout) impossible and added unnecessary latency to the happy path.

**Decision:** Check for both events in `process_element1` and `process_element2`. If both states are present when either event arrives, emit `confirmed` immediately and clear state. The timer still fires but finds empty state and becomes a no-op. This gives sub-second confirmation on the happy path while preserving the timeout/retry/DLQ behavior when `file.ready` never arrives.

---

## 🗑️ Delete Saga with Retry & Dead-Letter

### 24. SoftDelete-First Ordering Separates User Concern from Platform Concern

**Observation:** The original delete handler attempted MinIO `RemoveObject` first and emitted `FileDeleted` only on success. If MinIO was down, the user received `FileDeleteRejected` and had to manually retry — coupling the user experience to storage layer availability.

**Decision:** Reordered to soft-delete the DB row first (`UPDATE status='deleted'`), then emit `FileDeleted` immediately. The user sees instant feedback — the file vanishes from their view as soon as the DB update succeeds. MinIO cleanup is a separate platform concern: if `RemoveObject` fails, the handler emits `FileDeleteRetry` (with `retry_count=0`) instead of `FileDeleteRejected`. The retry loop operates silently in the background (up to 3 retries → dead-letter). `FileDeleteRejected` is now reserved exclusively for ownership/authorization failures, not transient infrastructure errors.

### 25. MinIO RemoveObject Is Idempotent — Returns nil for Non-Existent Objects

**Observation:** MinIO's `RemoveObject` returns `nil` (no error) when called on a key that doesn't exist. This is S3-compatible behavior (DELETE on a non-existent key is a no-op 204). This means E2E tests cannot trigger the DLQ path by producing a `FileDeleteRetry` event with a fake file path — the handler will "succeed" (nil error) and silently complete.

**Decision:** Unit tests with mock `ObjectRemover` returning errors are the correct way to verify retry/DLQ handler logic. The E2E test (`test_delete_retry_health.mjs`) validates infrastructure health — topics exist, schemas registered, consumer group offset advances — but cannot exercise the failure/DLQ path without injecting actual MinIO failures. This is acceptable: 20 unit tests cover all handler branches, and the E2E test confirms the plumbing is live.

### 26. rpk Topic Operations and Group Operations Require Separate ACL Grants

**Observation:** The E2E test produced a message to `internal.media.delete.retry` using the `flink-processor` SASL identity (which has topic produce/consume ACLs), then tried `rpk group describe media-service-consumer` with the same identity. This failed with `GROUP_AUTHORIZATION_FAILED` — the `flink-processor` user has topic-level ACLs but not group-level ACLs (`DescribeGroup`).

**Decision:** Use the `superuser` identity for administrative rpk commands like `rpk group describe` in E2E tests. Topic-level operations (produce, consume, list) and group-level operations (describe, list) are separate ACL categories in Kafka/Redpanda. A service identity's ACLs are scoped to its operational needs — `flink-processor` needs topic read/write but never needs to describe consumer groups.

### 27. Dead-Letter and Retry Schemas Must Mirror Full Event Payload

**Observation:** The Go delete handler produces `media_type` and `file_size` in the dead-letter event map, but the Avro schema for the DLQ topic omitted those fields. Avro serialization silently drops fields not present in the schema, so the DLQ Iceberg table lost data needed for future Spark orphan cleanup jobs.

**Decision:** Every DLQ/retry schema must carry the full payload from the original event. Schema completeness should be verified against the *producing* code, not just the consuming code. When adding a new field to an event producer, grep for all schemas that serialize from the same event map and add the field there too.

### 28. Flink CPU Over-Provisioning on Apple Silicon (8-core Mac)

**Observation:** All 5 Flink deployments (credit_check_processor, ttl_expiry_processor, move_saga_processor, media_notification_processor, identity processors) were using resource requests of 1000m CPU for JobManagers and 500m for TaskManagers, totaling 8,400m CPU requests across the cluster. On an 8-core Mac with 8,000m total allocatable CPU, this left 98% of the cluster capacity as requested (but not always actually scheduled). The move_saga_processor TaskManager pod failed to schedule because the node had no remaining capacity. Individual pod restarts would trigger cascading rescheduling failures during peak load.

**Decision:** Reduced JobManager CPU requests from 1000m to 250m (they are lightweight coordinators that don't process data). TaskManagers remain at 500m (they do the actual work). The total cluster CPU request dropped from 8,400m to 4,550m, leaving ~43% headroom on an 8-core Mac. This allows all 5 deployments to coexist without scheduling conflicts, while still providing sufficient capacity for per-pod memory (256Mi heap per TaskManager). On larger clusters (production), revert to higher JobManager CPU requests if Flink job compilation or checkpoint coordination becomes a bottleneck.

---

## 🖥️ Frontend Integration

### 28. Notification Waiter Must Match on Post-Saga Paths, Not Pre-Saga Paths

**Observation:** The frontend `notificationWaiter` was keyed on `uploads/...` (the path from `upload_ready`), but `upload_completed` carries `file_path: files/...` (post-move saga). The waiter timed out because the paths didn't match, even though the upload succeeded end-to-end.

**Decision:** When a saga transforms state (e.g., moves a file from `uploads/` to `files/`), the frontend waiter must register under the *final* path. Use multi-key registration (`[permanentPath, originalPath]`) to also match error notifications that reference the original path (e.g., `upload_expired` still carries `uploads/...`).

### 29. Realtime Event Handlers Must Refresh Dependent UI State

**Observation:** `credit.balance_changed` arrived via Realtime and appeared in Live Events, but the credit badge in the header didn't update because the handler only called `addNotification()`, not `refreshCredits()`.

**Decision:** When a Realtime notification implies a state change in a different UI component (credits, media browser), the handler must trigger a refresh of that component. Don't assume the user will reload. Map each notification `event_type` to its side effects: `credit.balance_changed` → `refreshCredits()`, `upload_completed` / `file_deleted` → `refreshMediaBrowser()`.

---

## 🔍 Observability & Instrumentation

### 1. zerolog `var log` Shadows stdlib `log` Package — Intentional

**Observation:** Each Go service declares `var log = zerolog.New(...).With().Timestamp().Logger()` at the package level, shadowing the stdlib `log` package.
**Decision:** This is intentional and allows gradual migration. Existing code using `log.Printf()` still works (referring to stdlib), while new code uses the structured logger. Eventually all calls migrate to `log.Info()` / `log.Error()`, and the shadowing becomes complete. No need to refactor the entire codebase at once.

### 2. `OTEL_EXPORTER_OTLP_ENDPOINT` Is Auto-Read By OTel SDK

**Observation:** The OTel SDK checks for `OTEL_EXPORTER_OTLP_ENDPOINT` environment variable automatically during initialization.
**Decision:** No custom parsing needed in `initOTel()`. The SDK reads the env var and configures the OTLP gRPC exporter endpoint. Default: `http://host.docker.internal:4317`.

### 3. message-consumer HTTP Listener on Port 8091 for K8s Probes

**Observation:** The message-consumer had no HTTP server before observability — it was a pure Kafka consumer. Adding K8s health probes required exposing `/healthz` and `/readyz`.
**Decision:** Added a second HTTP listener on port 8091 alongside the Kafka consumer, specifically for health checks. This keeps the Kafka consumer logic isolated and allows K8s to monitor readiness independently. The health check validates Kafka connectivity, so the pod is automatically restarted if the broker becomes unreachable.

### 4. Redpanda `/public_metrics` Requires Admin API Port (9644), Not Kafka Port

**Observation:** When adding Prometheus scrape targets, attempts to scrape Redpanda metrics on port 9092 (Kafka port) failed.
**Decision:** Redpanda exposes Prometheus metrics at port 9644 (admin API port), path `/public_metrics`. This is separate from the Kafka broker port. Prometheus scrape config must target `host.docker.internal:9644/public_metrics`.

### 5. MinIO Metrics Require `MINIO_PROMETHEUS_AUTH_TYPE=public` Environment Variable

**Observation:** MinIO metrics endpoint (`/minio/v2/metrics/cluster`) returns 401 Unauthorized by default.
**Decision:** Set `MINIO_PROMETHEUS_AUTH_TYPE=public` in the MinIO container environment to enable unauthenticated metrics access. In production, replace with proper auth (bearer token, TLS client cert) before exposing metrics to untrusted networks.

### 6. Health Checks Must Validate Downstream Dependencies

**Observation:** A service returning 200 OK from `/readyz` when Kafka is down causes the pod to remain in the load balancer and fail requests, degrading user experience.
**Decision:** The readiness probe validates actual dependency connectivity: check Kafka broker (via test produce/consume), check Postgres connection pool (SELECT 1). Return 503 if any check fails. K8s removes the pod from the load balancer immediately, preventing cascading failures.

---

## ⚙️ Batch Processing & Orchestration (Spark + Airflow)

### 1. macOS AirPlay Receiver Blocks Port 5000

**Observation:** macOS Monterey+ runs AirPlay Receiver on port 5000. Any service bound to `localhost:5000` via Docker port mapping is intercepted by Apple's AirTunes — `curl` returns a 403 with `Server: AirTunes/870.14.1` instead of reaching the container.
**Decision:** Moved Marquez API from port 5000 to 5050 (admin from 5001 to 5051). When choosing ports for new services, avoid 5000 and 7000 (macOS AirPlay and Screen Sharing defaults). Always test with `curl -v` to check the `Server` header when a service appears unresponsive.

### 2. Airflow 3.x Helm Chart: `apiServer` Replaces `webserver`

**Observation:** The Airflow Helm chart 1.19.0 (Airflow 3.x) renames the web-facing component from `webserver` to `apiServer`. Putting resource limits, startup probes, and other config under the `webserver:` key in `values.yaml` is silently ignored — the api-server pod runs with no resource limits, leading to OOMKills under memory pressure.
**Decision:** Resource limits, probes, and service config go under `apiServer:` in Helm values. The `webserver.defaultUser` key is still read by the `createUserJob` template for backward compatibility, so keep it for user creation. The service name also changed: `svc/airflow-webserver` → `svc/airflow-api-server`. The `workers` config moved from `[webserver]` to `[api]` section.

### 3. Airflow 3.x Provider Module Renames

**Observation:** The CNCF Kubernetes provider v10.x (bundled with Airflow 3.x) renamed `airflow.providers.cncf.kubernetes.operators.kubernetes_pod` to `airflow.providers.cncf.kubernetes.operators.pod`. The old import path causes a DAG parse error (0 DAGs, 1 error) that only appears in the dag-processor stats table — no traceback in the main log.
**Decision:** When upgrading Airflow major versions, check all DAG imports against the installed provider versions. Use `kubectl exec` to test imports directly inside the dag-processor pod. DAG parse errors in 3.x appear in the stats table (`# Errors` column), not as tracebacks in stdout.

### 4. KubernetesPodOperator Cross-Namespace RBAC

**Observation:** Airflow's `KubernetesPodOperator` runs task pods in a target namespace (e.g., `spark-apps`), but the Airflow worker pod runs in the `airflow` namespace. The worker's service account (`airflow-worker`) needs explicit RBAC (Role + RoleBinding) in the target namespace to create, list, and delete pods. Without it, the task fails with `403 Forbidden: pods is forbidden`.
**Decision:** Create a Role with `pods`, `pods/log`, `pods/status` and `events` permissions in the target namespace, bound to the `airflow-worker` ServiceAccount in the `airflow` namespace. This is a one-time setup per target namespace.

### 5. Marquez Requires Mounted Config File

**Observation:** Setting `MARQUEZ_CONFIG=/etc/marquez/config.yml` in docker-compose without mounting a file at that path causes Marquez to crash with `FileNotFoundException`. The built-in default config at `marquez.dev.yml` hardcodes `postgres` as the DB hostname, which doesn't match a custom service name like `marquez-db`.
**Decision:** Mount a custom `config.yml` into the container with env-var-templated DB connection settings (`${POSTGRES_HOST:-marquez-db}`). This keeps the docker-compose service naming flexible while satisfying the config requirement.

### 6. KubernetesPodOperator Log Retrieval After Pod Deletion

**Observation:** With `is_delete_operator_pod=True`, the task pod is cleaned up after execution. Airflow then tries to fetch logs from the deleted pod's hostname, resulting in `NameResolutionError`. The actual task logs were streamed during execution (`get_logs=True`) but aren't persisted after pod deletion.
**Decision:** This is expected behavior for local dev. In production, configure remote logging (S3/GCS) in Airflow so task logs survive pod deletion. The `get_logs=True` setting streams logs to Airflow's task log during execution — they're visible in the UI while the task runs, just not after the pod is gone.

### 7. Spark Docker Image PYTHONPATH for PySpark + py4j

**Observation:** The `apache/spark:4.0.2-python3` image bundles PySpark at `/opt/spark/python` and py4j as a zip at `/opt/spark/python/lib/py4j-0.10.9.9-src.zip`. The default Spark entrypoint (`/opt/entrypoint.sh`) sets PYTHONPATH, but clearing the entrypoint (needed for running pytest directly) loses this setup. Without both paths, imports fail with `No module named pyspark` or `No module named py4j`.
**Decision:** In the Dockerfile test stage, set `ENV PYTHONPATH="/opt/spark/python:/opt/spark/python/lib/py4j-0.10.9.9-src.zip"` explicitly and use `ENTRYPOINT []` + `CMD ["python3", "-m", "pytest", ...]`. Don't append `${PYTHONPATH}` — it's undefined in the base image and Docker emits a warning.

---

## 📊 Telemetry Configuration

### 1. Airflow 3.x OTel Metrics Use `_total` Suffix on Counters

**Observation:** Airflow 3.x ships with built-in OpenTelemetry support. When exporting metrics via OTLP, all counter metrics follow the OpenTelemetry naming convention and append `_total` to the metric name (e.g., `airflow_scheduler_heartbeat_total`, not `airflow_scheduler_heartbeat`). This differs from the StatsD metric names documented in older Airflow versions.
**Decision:** When building Grafana dashboards or alert rules for Airflow OTel metrics, always verify actual metric names via `curl localhost:9090/api/v1/query?query={__name__=~"airflow.*"}` before writing PromQL queries. Gauge metrics (e.g., `airflow_dagbag_size`, `airflow_executor_open_slots`) keep their original names; only counters get the `_total` suffix.

### 2. Grafana Provisioned Datasource UIDs Must Be Explicit

**Observation:** Grafana auto-generates random UIDs for provisioned datasources when no `uid:` field is specified in `datasources.yaml` (e.g., `PBFA97CFB590B2093`). Provisioned dashboards referencing `"uid": "prometheus"` silently fail to resolve the datasource — panels render as "No data" with no error in the UI. Changing the UID after initial provisioning causes Grafana to crash on startup with `Datasource provisioning error: data source not found`.
**Decision:** Always set explicit `uid:` fields in `telemetry/grafana/provisioning/datasources/datasources.yaml` (`prometheus`, `loki`, `jaeger`) to match what dashboards reference. If changing UIDs on an existing Grafana instance, force-recreate the container (`docker compose up -d --force-recreate grafana`) to clear the stale internal state. Ephemeral Grafana storage makes this safe for local dev.

---

## 🔬 Linting & Static Analysis

### 1. golangci-lint v2 Config Format

**Observation:** golangci-lint v2 introduced breaking config changes. It requires `version: "2"` at the top of `.golangci.yml`. `goimports` is now a **formatter** (under `formatters.enable`), not a linter. Linter settings moved under `linters.settings`. Test file exclusions use an `exclusions` block with `rules` and `paths`.
**Decision:** Config at `go-services/.golangci.yml` uses the v2 schema. Run `golangci-lint run ./...` per service directory.

### 2. sqlfluff Has No Flink SQL Dialect

**Observation:** sqlfluff supports `postgres`, `ansi`, `sparksql`, and many dialects — but not Flink SQL. Flink SQL uses backtick identifiers, JDBC `WITH (...)` connector syntax, and `${}` env var placeholders that no dialect can parse. Even with `ignore = parsing`, the number of false positives made linting useless.
**Decision:** Only lint Supabase migrations (postgres dialect). Flink SQL files are skipped entirely. The `.sqlfluff` config and Taskfile `lint:sql` task reflect this.

### 3. Mermaid Diagram Syntax for Linter Compatibility

**Observation:** The `@probelabs/maid` linter enforces stricter syntax than the mermaid renderer accepts. Key rules: (a) edge labels must not use quotes inside pipes — `-->|text|` not `-->|"text"|`, (b) cylinder node labels `[(text)]` must not have inner quotes — `[(text)]` not `[("text")]`, (c) parentheses inside edge labels break the parser — rephrase to avoid `()` in `|...|`.
**Decision:** All `.mmd` files follow linter-compatible syntax. This is stricter than what mermaid renders but ensures automated validation catches real errors. `click` statements use the `href` keyword form.

### 4. markdownlint Rules Disabled for Documentation Projects

**Observation:** Out of 1,716 initial violations, 963 were MD013 (line length at 80 chars) and 224 were MD060 (table column spacing). These rules are designed for prose writing, not technical documentation with code blocks, URLs, and compact tables.
**Decision:** Config at `.markdownlint-cli2.yaml` disables: MD013 (line-length), MD033 (inline HTML), MD041 (first-line heading), MD060 (table column style), MD036 (emphasis-as-heading), MD028 (blank line in blockquote — false positive on GFM callouts). AGENT.md symlinks excluded to avoid double-linting.

### 5. ruff target-version Must Account for Mixed Python Runtimes

**Observation:** ruff's `target-version` controls which pyupgrade (`UP`) rules are applied. With `target-version = "py312"`, rule UP017 replaces `datetime.timezone.utc` with `datetime.UTC` — but `datetime.UTC` was introduced in Python 3.11. The PyFlink Docker image runs Python 3.10, so this auto-upgrade causes `ImportError: cannot import name 'UTC' from 'datetime'` at runtime, crashing all 3 PyFlink processors.
**Decision:** Keep `target-version = "py312"` globally (most code runs 3.12), but suppress `UP017` for `pyflink_jobs/*.py` via `per-file-ignores` in `pyproject.toml`. This is narrower than lowering the global target, which would disable all modern syntax upgrades for 3.12 code. When the PyFlink image upgrades to 3.11+, the suppression can be removed.

### 6. mypy Requires explicit_package_bases for Non-Standard Layouts

**Observation:** Running mypy across multiple Python directories (`pyspark_apps/`, `pyflink_jobs/`, `flink_jobs/`, `airflow/`) causes "duplicate module name" and "source file found twice under different module names" errors. This happens because multiple directories contain `identity/` packages or standalone scripts without `__init__.py`.
**Decision:** Set `explicit_package_bases = true` in `[tool.mypy]` and pass `--explicit-package-bases` in the Taskfile. Airflow errors suppressed via `[[tool.mypy.overrides]]` since Airflow stubs aren't installed locally.

## 📦 Project Philosophy

### 1. The "Base Stack" Strategy

**Observation:** There is a tendency to rename repositories every time a new product (e.g., SpaceTaxi) is built on top.
**Decision:** Maintain the core repository as `event-substrate`. New products are built as **branches** or **consumers** on top of this chassis, preserving the proven infrastructure while allowing rapid product iteration.
