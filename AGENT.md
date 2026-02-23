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
task start       # Start platform (incremental Go rebuilds)
task test:e2e    # Run all end-to-end pipeline tests
task shutdown    # Stop everything
```

## Coding Conventions

**Go:** Config via `spf13/viper` mapped to `Config{}` structs — never raw `os.Getenv` calls (exception: `api-gateway/main.go` uses `os.Getenv` for SASL fields). Use `hamba/avro`, `twmb/franz-go`, `lib/pq` — no ORMs. Kafka consumer failures must crash the pod (`log.Fatalf`) to prevent offset advancement and silent data loss. Viper requires `SetDefault()` for all Config struct fields or they won't bind from env vars.

**Kafka Authentication:** Every service authenticates to Redpanda via SASL/SCRAM-SHA-256 with least-privilege ACLs. Env vars: `KAFKA_SASL_MECHANISM`, `KAFKA_SASL_USERNAME`, `KAFKA_SASL_PASSWORD` (+ `KAFKA_SECURITY_PROTOCOL` for Flink). Identities and ACLs are in `scripts/setup-redpanda-auth*.sh`. To swap to OAUTHBEARER in production, change the mechanism env var — no code changes needed.

**Go Docker rebuilds:** `docker compose restart` does NOT recompile. Always `docker compose build --no-cache <service> && docker compose up -d <service>`.

**Flink SQL:** Kafka source AND sink tables are registered centrally by `sql_runner.py` — SQL files only define JDBC sinks and INSERT logic. `${ENV_VAR}` placeholders are resolved at runtime. All INSERT statements are aggregated into a single `statement_set`. Notification sinks must be append-only (no PRIMARY KEY = pure INSERT, not UPSERT). An Iceberg catalog (Nessie REST) is also registered for querying tiered storage tables. SASL JAAS config must use the **shaded** class path: `org.apache.flink.kafka.shaded.org.apache.kafka.common.security.scram.ScramLoginModule`. SQL files cannot contain JAAS configs because the `;` in the JAAS string conflicts with sql_runner.py's statement splitting.

**Avro schemas:** Live in `avro/` with directory paths mapping to Kafka topic names (e.g., `avro/public/identity/login.events.avsc` → topic `public.identity.login.events`). Renaming topics requires re-registering schemas under the new `<topic>-value` subject.

**Supabase migrations:** Always set `REPLICA IDENTITY FULL` on tables consumed by Realtime. The `user_notifications` table uses `payload TEXT` (not JSONB) for Flink JDBC compatibility. Event types follow `{domain}.{entity}` convention (e.g., `identity.login`, `user.message`). Message visibility is controlled by `visibility` (`broadcast` | `direct`) and `recipient_id` (nullable UUID) columns — RLS enforces that direct messages are only visible to sender + recipient.

**Frontend:** Single Realtime subscription on `user_notifications`. Must REST-prefetch history concurrently with WebSocket setup due to Flink processing outpacing socket negotiation. Message payloads include `visibility: 'broadcast'` and `recipient_id: null` for broadcast messages; use `visibility: 'direct'` with a target UUID for private delivery.

## Deployment Checklist
After modifying code, always complete the full deployment cycle before testing:
1. **Go changes:** Rebuild Docker image (`task build:consumer` or `task build:gateway`), then restart K8s pod (`kubectl rollout restart deployment <name> -n go-microservices`)
2. **Flink SQL / sql_runner.py changes:** Re-apply configmaps (`task k8s:configmaps`), then delete + re-apply the FlinkDeployment (`kubectl delete -f kubernetes/flink-deployment.yaml && kubectl apply -f kubernetes/flink-deployment.yaml`)
3. **Migration changes:** Run `supabase db reset` to re-apply from scratch if migrations were added/deleted
4. **Always run `task test:e2e`** after pipeline-affecting changes to validate end-to-end flow

## Key Knowledge
- Query **ChromaDB** for past learnings before major architectural changes or debugging recurring issues (skip if unavailable)
- **Redpanda Auth:** SASL/SCRAM enabled via `redpanda-bootstrap.yaml` (cluster-level). The `redpanda-auth-init` Docker service creates users/ACLs before Schema Registry starts. ACLs enforce `public.*` (open read) vs `internal.*` (Flink/ClickHouse only) topic boundaries. Admin API (port 9644) is unauthenticated — used only for bootstrapping SASL users
- Topic routing is strictly allowlisted via `go-services/api-gateway/routes.yaml` (hot-reloaded by fsnotify)
- All processors write exclusively to `user_notifications` for unified egress (no domain tables)
- **Testing:** After changes to Go services, Flink SQL, migrations, or frontend event handling, run `task test:e2e` to validate the full pipeline. Skip for doc-only or config-only changes. When fixing a bug, evaluate whether a new E2E test case is needed to prevent regression — add one to `tests/e2e/` if the bug could recur silently
- **Documentation:** After ANY code or infrastructure change, review and update ALL affected project docs before considering the task complete. The doc set is: `README.md` (user-facing flows, task table), `architecture.md` (data flow steps, diagrams), `AGENT.md` (conventions, deployment checklist, key knowledge), `learnings.md` (gotchas and decisions), and `productionization.md` (secrets inventory, hardcoded values, production gaps). If a change touches schemas, RLS policies, auth, or new env vars, at least 3 of these docs likely need updates. Never assume "just the code" is enough
- See [productionization.md](./productionization.md) for cloud deployment checklist
