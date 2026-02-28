# Agent Guidelines

Event-driven microservices platform. See [README.md](./README.md) for architecture overview, [architecture.md](./docs/architecture.md) for data flow, [learnings.md](./docs/learnings.md) for gotchas, [productionization.md](./docs/productionization.md) for cloud deployment, [github_cicd_plan.md](./docs/github_cicd_plan.md) for CI/CD, [data_governance_plan.md](./docs/data_governance_plan.md) for governance, and [data_processing_plan.md](./docs/data_processing_plan.md) for Spark/Airflow.

Service-specific conventions live in each sub-folder's `AGENT.md`:

- `go-services/AGENT.md` — Go, Docker, media endpoints, credit economy
- `flink_jobs/AGENT.md` — Flink SQL, sql_runner.py, JAAS
- `frontend/AGENT.md` — Vite, vitest, Playwright, media UI
- `supabase/AGENT.md` — migrations, RLS, Realtime, event types

Custom skills live in `.claude/skills/<name>/SKILL.md`:

- `/tdd-execute` — Red-Green-Refactor subagent loop (co-locates `tdd-protocol.md`)
- `/feature-epic` — multi-domain feature decomposition + sequential TDD
- `/agent-team` — cost-effective multi-agent orchestration with model selection
- `/bug-fix` — orchestrated diagnosis and fix with review gates
- `/ship` — group dirty working tree into sequential PRs
- `/pr` — push changes to a branch, open a PR, wait for CI, then merge
- `/mermaid-to-svg` — convert `.mmd` to `.svg` via CLI

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

**Linting & Code Quality:** Every domain has static analysis configured. Run `task lint` (all domains) or individual `task lint:{go,python,frontend,sql,yaml,k8s,markdown,mermaid}`. Auto-fix with `task lint:fix`. Type checking via `task check:types` (Python/mypy). Security scanning via `task check:security` (ruff S rules + npm audit + gosec). Composite `task check` runs lint + type-check. Tools: golangci-lint (Go), ruff (Python), ESLint (Frontend), sqlfluff (SQL), yamllint (YAML), kubeconform (K8s), markdownlint-cli2 (Markdown), @probelabs/maid (Mermaid). TDD protocol enforces lint as a GREEN phase exit criterion.

**Containerized Builds:** All lint, type-check, test, and security tasks run inside Docker builder images (`docker/builders/`). This ensures identical tool versions locally and in CI. Builder images: `go-builder` (Go 1.26 + golangci-lint v2.10.1), `pyflink-builder` (Python 3.10 + ruff + mypy), `pyspark-builder` (Python 3.12 + ruff + mypy + pyspark + pytest), `frontend-builder` (Node.js 22), `infra-lint` (yamllint, sqlfluff, kubeconform, markdownlint-cli2, maid, helm). Build all with `task build:builders`. Named volumes (`event-substrate-gomod`, `event-substrate-frontend-nm`) cache dependencies between runs. CI uses the same images from GHCR via `container:` directives. Python linting is split by runtime: `lint:python:flink` (py310) and `lint:python:spark` (py312). See `docs/learnings.md` "Containerized Builder Images" for gotchas.

## Deployment Checklist

1. **Go changes:** `task build:consumer` or `task build:gateway` → `task helm:install` → `kubectl rollout restart deployment <name> -n go-microservices`. Wait ~15s.
2. **Flink SQL changes:** `task k8s:configmaps` → delete + re-apply FlinkDeployment.
3. **Migration changes:** `supabase db reset`
4. **MinIO webhook changes:** `docker restart minio` → re-run `scripts/setup-minio-webhook.sh`
5. **Airflow Helm values changes:** `task airflow:upgrade`
6. **Grafana dashboard/alert/datasource changes:** `docker compose up -d --force-recreate grafana`
7. **Helm chart changes:** `task helm:template` (dry-run) → `task helm:install`
8. **Spark job changes:** `task spark:build:identity` (or relevant app) → `task helm:install`
9. **Before E2E tests:** `task start` (idempotent reconciliation)
10. **Always run `task test:e2e`** after pipeline-affecting changes.
11. **Persistent failures:** `task purge && task init`

## Key Knowledge

- **TDD Workflow:** Use `/tdd-execute` for new endpoints, functions, bug fixes with reproducible failures. Use `/feature-epic` for multi-domain features (breaks into domains, runs TDD per domain). Add `/agent-team` to either for cost-effective model selection and escalation. Skip TDD for config/YAML, migrations, docs, one-line fixes.
- **Testing:** E2E tests in `tests/e2e/` are mandatory for every feature. Plan them explicitly in Phase 1 of `/feature-epic`. Run `task start` then `task test:e2e` after pipeline changes.
- **Architecture Diagram:** Update diagrams in `docs/architecture/` and regenerate SVGs (run `/mermaid-to-svg`) after any topology change. Mandatory alongside code changes. Overview: `docs/architecture/overview.mmd`. Detail diagrams: `media-upload-saga.mmd`, `media-download-delete.mmd`, `identity-messaging.mmd`, `analytics.mmd`. Mermaid syntax must pass `task lint:mermaid` — no quotes in edge labels `-->|text|`, no quotes in cylinder labels `[(text)]`, avoid parentheses inside edge labels.
- **Documentation:** After any change, update all affected docs: `README.md`, `docs/architecture.md`, `docs/architecture/*.mmd`, `AGENT.md`, `docs/learnings.md`, `docs/productionization.md`, `docs/github_cicd_plan.md`, `docs/data_governance_plan.md`, and `docs/data_processing_plan.md`.
