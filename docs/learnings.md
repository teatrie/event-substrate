# Project Learnings & Architectural Decisions

This document captures the hard-won wisdom, "gotchas," and strategic decisions made during the development of **The Event-Driven Substrate**. 

---

## 🛠️ Infrastructure & Local Development

### 1. High-Performance Local Mirroring
**Observation:** Running a full production-grade stack (Redpanda, Flink, ClickHouse, OTel, Spark) on a local machine requires significant resource efficiency.
**Decision:** Standardized on **OrbStack** for Apple Silicon. Its native virtualization layer handle the Redpanda tiered storage (S3/MinIO) and Kubernetes management with significantly lower overhead than traditional local Docker Desktop solutions.

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

**Decision:** Added a `warmup.mjs` canary test to `run_all.sh` that signs up a throwaway user and polls for the `identity.signup` notification with a generous 3-minute timeout. If the canary succeeds, Flink is ready and all subsequent tests use standard 30s timeouts. If it fails, the pipeline is genuinely broken — not just slow. This eliminates false negatives from cold-start timing without inflating every test's timeout.

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

## 📦 Project Philosophy

### 1. The "Base Stack" Strategy
**Observation:** There is a tendency to rename repositories every time a new product (e.g., SpaceTaxi) is built on top.
**Decision:** Maintain the core repository as `event-substrate`. New products are built as **branches** or **consumers** on top of this chassis, preserving the proven infrastructure while allowing rapid product iteration.
