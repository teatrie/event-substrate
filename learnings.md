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

## 📦 Project Philosophy

### 1. The "Base Stack" Strategy
**Observation:** There is a tendency to rename repositories every time a new product (e.g., SpaceTaxi) is built on top.
**Decision:** Maintain the core repository as `event-substrate`. New products are built as **branches** or **consumers** on top of this chassis, preserving the proven infrastructure while allowing rapid product iteration.
