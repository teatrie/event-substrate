# Agent Guidelines

Event-driven microservices platform. See [README.md](./README.md) for architecture overview, [architecture.md](./architecture.md) for data flow, and [learnings.md](./learnings.md) for gotchas.

## Tech Stack
- **Go** microservices (`api-gateway`, `message-consumer`) in `go-services/`
- **Apache Flink** SQL + Python stream processors in `flink_jobs/`, `pyflink_jobs/`
- **Redpanda** (Kafka-compatible) broker with **Confluent Avro** serialization
- **Supabase** (Postgres + Auth + Realtime) in `supabase/`
- **ClickHouse** OLAP warehouse, **MinIO** + **Nessie** data lake
- **Vite** vanilla JS frontend in `frontend/`
- **Kubernetes** manifests in `kubernetes/`, Docker Compose for infra

## Commands
This project uses [Task](https://taskfile.dev) (`go-task`) as its build system. See `Taskfile.yml` for all available targets. Key commands:
```
task init        # First-time full setup
task start       # Start platform (idempotent — reconciles schemas, topics, configmaps)
task test:e2e    # Run all end-to-end pipeline tests
task purge       # Nuclear teardown — destroy all state, then run 'task init'
task shutdown    # Stop everything
```

## Coding Conventions

**Go:** Config via `spf13/viper` mapped to `Config{}` structs — never raw `os.Getenv` calls (exception: `api-gateway/main.go` uses `os.Getenv` for SASL fields). Use `hamba/avro`, `twmb/franz-go`, `lib/pq` — no ORMs. Kafka consumer failures must crash the pod (`log.Fatalf`) to prevent offset advancement and silent data loss. Viper requires `SetDefault()` for all Config struct fields or they won't bind from env vars.

**Kafka Authentication:** Every service authenticates to Redpanda via SASL/SCRAM-SHA-256 with least-privilege ACLs. Env vars: `KAFKA_SASL_MECHANISM`, `KAFKA_SASL_USERNAME`, `KAFKA_SASL_PASSWORD` (+ `KAFKA_SECURITY_PROTOCOL` for Flink). Identities and ACLs are in `scripts/setup-redpanda-auth*.sh`. To swap to OAUTHBEARER in production, change the mechanism env var — no code changes needed.

**Go Docker rebuilds:** `docker compose restart` does NOT recompile. Always `docker compose build --no-cache <service> && docker compose up -d <service>`.

**Dockerfiles — keep build context in sync:** When adding, renaming, or moving source files in any service, verify its Dockerfile (and `.dockerignore`) copies them into the build context. Prefer glob patterns (`COPY *.go ./`, `COPY src/ ./src/`) over hardcoded filenames (`COPY main.go ./`). Missing files cause build failures that only surface at Docker build time, not during local builds.

**Flink SQL:** Kafka source AND sink tables are registered centrally by `sql_runner.py` — SQL files only define JDBC sinks and INSERT logic. `${ENV_VAR}` placeholders are resolved at runtime. All INSERT statements are aggregated into a single `statement_set`. Notification sinks must be append-only (no PRIMARY KEY = pure INSERT, not UPSERT). An Iceberg catalog (Nessie REST) is also registered for querying tiered storage tables. SASL JAAS config must use the **shaded** class path: `org.apache.flink.kafka.shaded.org.apache.kafka.common.security.scram.ScramLoginModule`. SQL files cannot contain JAAS configs because the `;` in the JAAS string conflicts with sql_runner.py's statement splitting.

**Avro schemas:** Live in `avro/` with directory paths mapping to Kafka topic names (e.g., `avro/public/identity/login.events.avsc` → topic `public.identity.login.events`). Renaming topics requires re-registering schemas under the new `<topic>-value` subject.

**Supabase migrations:** Always set `REPLICA IDENTITY FULL` on tables consumed by Realtime. The `user_notifications` table uses `payload TEXT` (not JSONB) for Flink JDBC compatibility. Event types follow `{domain}.{entity}` convention (e.g., `identity.login`, `user.message`). Message visibility is controlled by `visibility` (`broadcast` | `direct`) and `recipient_id` (nullable UUID) columns — RLS enforces that direct messages are only visible to sender + recipient.

**Frontend:** Single Realtime subscription on `user_notifications`. Must REST-prefetch history concurrently with WebSocket setup due to Flink processing outpacing socket negotiation. Message payloads include `visibility: 'broadcast'` and `recipient_id: null` for broadcast messages; use `visibility: 'direct'` with a target UUID for private delivery. Media upload UI in `frontend/media.js`. Tests use vitest.

**Media Upload (Claim Check Pattern):** Raw files stay in MinIO/GCS — only metadata events flow through Kafka/Flink. The API Gateway generates presigned PUT URLs (`upload_handler.go`) and never proxies file bytes. MinIO webhook events are transformed to Avro by `media_webhook_handler.go` at `/webhooks/media-upload` (no auth — network-isolated). Credits are deducted on upload *completion* (async webhook), not at URL request time. See [learnings.md](./learnings.md) for MinIO gotchas (host rewriting, URL encoding, JSON number coercion, retry queues).

**Media Download:** `POST /api/v1/media/download-url` (JWT-authenticated, `download_handler.go`). Verifies file ownership via `PostgresFileStore.GetFileMetadata()` (queries `media_files` WHERE `status = 'active'`), generates a presigned GET URL via `MinioURLSigner.GeneratePresignedGET()`, emits `FileDownloaded` event to `public.media.download.events`, returns `{"download_url": "...", "expires_in": 900}`.

**Media Delete:** `POST /api/v1/media/delete` (JWT-authenticated, `delete_handler.go`). Verifies file ownership, soft-deletes (`UPDATE media_files SET status = 'deleted'`), emits `FileDeleted` event to `public.media.delete.events`, returns `{"success": true}`. No credit refund. RLS filters `status = 'active'` so deleted files vanish immediately.

**Shared Media Dependencies:** `media_deps.go` defines `FileStore` interface (`VerifyOwnership`, `SoftDelete`, `GetFileMetadata`), `FullURLSigner` interface (extends `URLSigner` with `GeneratePresignedGET`), `EventProducer` function type, and `authenticateRequest()` shared JWT auth helper. `upload_deps.go` has `PostgresFileStore`, `MinioURLSigner` (implements both PUT and GET), and `initMediaHandlers()` which creates all three handlers (upload, download, delete) sharing a single DB pool and MinIO client.

**Credit Economy:** `credit_ledger` (append-only, INSERT-only) + `user_credit_balances` (aggregated SUM view). Signup grants +2 credits, each upload costs 1 credit. Event type: `credit.balance_changed`. The `media_files` table uses a `status` column for soft-delete support.

## Deployment Checklist
After modifying code, always complete the full deployment cycle before testing:
1. **Go changes:** Rebuild Docker image (`task build:consumer` or `task build:gateway`), then restart K8s pod (`kubectl rollout restart deployment <name> -n go-microservices`). Wait ~15s for rollout before testing
2. **Flink SQL / sql_runner.py changes:** Re-apply configmaps (`task k8s:configmaps`), then delete + re-apply the FlinkDeployment (`kubectl delete -f kubernetes/flink-deployment.yaml && kubectl apply -f kubernetes/flink-deployment.yaml`)
3. **Migration changes:** Run `supabase db reset` to re-apply from scratch if migrations were added/deleted
4. **MinIO bucket/webhook changes:** Restart the `minio-init` service (`docker compose restart minio-init`) and re-run `scripts/setup-minio-webhook.sh` if webhook config changed. Note: MinIO requires `docker restart minio` followed by re-running the webhook setup script — `mc admin config set` changes only take effect after a MinIO restart
5. **Before E2E tests:** Run `task start` to ensure full state reconciliation (schemas, topics, configmaps). This is idempotent and handles container restarts/rebuilds that may have wiped Redpanda or other external state
6. **Always run `task test:e2e`** after pipeline-affecting changes to validate end-to-end flow
7. **Persistent failures:** If E2E tests fail despite `task start`, run `task purge` followed by `task init` to rebuild from scratch

## Slash Commands (`.claude/commands/`)
Reusable workflows live in `.claude/commands/*.md`. Before implementing a common task from scratch, check if a command exists. Key commands:
- `/mermaid-to-svg` — convert `.mmd` files to validated SVG (uses `@mermaid-js/mermaid-cli`, NOT the Mermaid Chart MCP tool)
- `/feature-epic` — multi-domain feature planning
- `/tdd-execute` — Red-Green-Refactor via subagents (see `.claude/agents/tdd-*.md`)

## Key Knowledge
- Query **ChromaDB** for past learnings before major architectural changes or debugging recurring issues (skip if unavailable)
- **Redpanda Auth:** SASL/SCRAM enabled via `redpanda-bootstrap.yaml` (cluster-level). The `redpanda-auth-init` Docker service creates users/ACLs before Schema Registry starts. ACLs enforce `public.*` (open read) vs `internal.*` (Flink/ClickHouse only) topic boundaries
- Topic routing is strictly allowlisted via `go-services/api-gateway/routes.yaml` (hot-reloaded by fsnotify)
- All processors write exclusively to `user_notifications` for unified egress (no domain tables). Exception: `credit_balance_processor` also writes to `credit_ledger` and `media_files` via JDBC sinks
- **JWT Authentication:** The API Gateway validates JWTs via shared `jwtKeyFunc` supporting HS256 (`SUPABASE_JWT_SECRET`) and ES256 (`SUPABASE_JWKS_URL`). Both `externalHandler` and `UploadHandler` use it
- **Media Upload env vars:** `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET`, `SUPABASE_DB_URL`. Webhook setup: `scripts/setup-minio-webhook.sh` (must run after MinIO restart)
- **Media Download/Delete:** Endpoints wired directly in `main.go` (not via routes.yaml). Both emit Kafka events (`public.media.download.events`, `public.media.delete.events`) via `produceEventDirect()` which creates synthetic requests through `processAndProduceEvent`. Avro schemas in `avro/public/media/`. Topics auto-created by `task topics:create`
- **TDD Workflow:** For new testable functionality (endpoints, functions, modules, bug fixes with reproducible failures), recommend `/tdd-execute` during planning. It orchestrates Red-Green-Refactor via subagents in `.claude/agents/tdd-*.md`. Do not recommend for config/YAML, migrations, docs, one-line fixes, or infrastructure changes
- **Testing:** New features and bug fixes are NOT complete until E2E tests exist in `tests/e2e/`. This is a mandatory deliverable, not an afterthought. For multi-domain features (`/feature-epic`), E2E tests must be planned as an explicit domain in Phase 1 — never deferred to "after implementation." Run `task start` then `task test:e2e` after changes to Go services, Flink SQL, migrations, or frontend event handling. Skip for doc-only or config-only changes. When fixing a bug, add a regression test unless the bug is trivially unrepeatable. Update `run_all.sh`, `Taskfile.yml`, and `helpers.mjs` as needed
- **Architecture Diagram:** `architecture.mmd` is the Mermaid source of truth for the system diagram. After ANY change that adds new services, tables, topics, data flows, or event types, update `architecture.mmd` AND regenerate `architecture.svg` (run `/mermaid-to-svg`). This is a mandatory deliverable alongside doc updates — the diagram must always reflect the current system topology
- **Documentation:** After ANY code or infrastructure change, review and update ALL affected project docs before considering the task complete. The doc set is: `README.md` (user-facing flows, task table), `architecture.md` (data flow steps, diagrams), `architecture.mmd` (Mermaid diagram source → regenerate SVG), `AGENT.md` (conventions, deployment checklist, key knowledge), `learnings.md` (gotchas and decisions), and `productionization.md` (secrets inventory, hardcoded values, production gaps). If a change touches schemas, RLS policies, auth, or new env vars, at least 3 of these docs likely need updates. Never assume "just the code" is enough
- See [productionization.md](./productionization.md) for cloud deployment checklist
