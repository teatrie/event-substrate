# Agent Guidelines

Event-driven microservices platform. See [README.md](./README.md) for architecture overview, [architecture.md](./architecture.md) for data flow, [learnings.md](./learnings.md) for gotchas, and [productionization.md](./productionization.md) for cloud deployment.

Service-specific conventions live in each sub-folder's `AGENT.md`:
- `go-services/AGENT.md` — Go, Docker, media endpoints, credit economy
- `flink_jobs/AGENT.md` — Flink SQL, sql_runner.py, JAAS
- `frontend/AGENT.md` — Vite, vitest, Playwright, media UI
- `supabase/AGENT.md` — migrations, RLS, Realtime, event types

## Domain Boundaries (DDD)

This platform is organized around explicit domain ownership. Violating these boundaries creates tight coupling and breaks the event-driven contract:

- **Event naming:** `{domain}.{entity}.{action}` — e.g., `identity.login`, `user.message`, `credit.balance_changed`, `media.file.uploaded`
- **Topic ownership:** Each domain writes only to its own `public.{domain}.*` topics. No service writes directly to another domain's topics.
- **Cross-domain communication:** Always via Kafka topics — never via direct DB reads across domain tables. Flink processors consume events, not tables.
- **Schema ownership:** Each domain owns its Avro schemas under `avro/{visibility}/{domain}/`. Schema changes are breaking changes and require a new topic subject.
- **Egress exception:** All processors funnel notifications through `user_notifications` (unified egress) — this is the single sanctioned cross-domain write path.

## Cross-Cutting Conventions

**Kafka Authentication:** Every service authenticates to Redpanda via SASL/SCRAM-SHA-256 with least-privilege ACLs. Env vars: `KAFKA_SASL_MECHANISM`, `KAFKA_SASL_USERNAME`, `KAFKA_SASL_PASSWORD` (+ `KAFKA_SECURITY_PROTOCOL` for Flink). Identities and ACLs are in `scripts/setup-redpanda-auth*.sh`. To swap to OAUTHBEARER in production, change the mechanism env var — no code changes needed.

**Avro schemas:** Live in `avro/` with directory paths mapping to Kafka topic names (e.g., `avro/public/identity/login.events.avsc` → topic `public.identity.login.events`). Renaming topics requires re-registering schemas under the new `<topic>-value` subject.

## Deployment Checklist
1. **Go changes:** `task build:consumer` or `task build:gateway` → `kubectl rollout restart deployment <name> -n go-microservices`. Wait ~15s.
2. **Flink SQL changes:** `task k8s:configmaps` → delete + re-apply FlinkDeployment.
3. **Migration changes:** `supabase db reset`
4. **MinIO webhook changes:** `docker restart minio` → re-run `scripts/setup-minio-webhook.sh`
5. **Before E2E tests:** `task start` (idempotent reconciliation)
6. **Always run `task test:e2e`** after pipeline-affecting changes.
7. **Persistent failures:** `task purge && task init`

## Key Knowledge
- **TDD Workflow:** Use `/tdd-execute` for new endpoints, functions, bug fixes with reproducible failures. Use `/feature-epic` for multi-domain features (breaks into domains, runs TDD per domain). Add `/agent-team` to either for cost-effective model selection and escalation. Skip TDD for config/YAML, migrations, docs, one-line fixes.
- **Testing:** E2E tests in `tests/e2e/` are mandatory for every feature. Plan them explicitly in Phase 1 of `/feature-epic`. Run `task start` then `task test:e2e` after pipeline changes.
- **Architecture Diagram:** Update `architecture.mmd` and regenerate `architecture.svg` (run `/mermaid-to-svg`) after any topology change. Mandatory alongside code changes.
- **Documentation:** After any change, update all affected docs: `README.md`, `architecture.md`, `architecture.mmd`, `AGENT.md`, `learnings.md`, `productionization.md`.
