# Productionization Guide

This document is a comprehensive checklist and reference for deploying the Event-Driven Substrate to a production cloud environment. It covers two target platforms: **Google Cloud Platform (GCP)** and **Amazon Web Services (AWS)**.

> [!IMPORTANT]
> The local development stack runs entirely on OrbStack (Docker Compose + Kubernetes) with hardcoded `host.docker.internal` addresses, plaintext credentials, disabled TLS, and single-replica deployments. **Every section below must be addressed before any production traffic flows.**

---

## 1. Infrastructure Replacements

All Docker Compose services must be replaced with cloud-managed equivalents. Self-hosting stateful infrastructure in production carries extreme operational overhead.

| Local Service | GCP Replacement | AWS Replacement | Notes |
|---|---|---|---|
| **Redpanda** (Kafka-compatible broker) | Confluent Cloud or Redpanda Serverless | Amazon MSK Serverless or Confluent Cloud | Must support Kafka protocol, Avro, and consumer groups |
| **Schema Registry** (Confluent) | Confluent Cloud Schema Registry | Confluent Cloud Schema Registry or AWS Glue Schema Registry | Glue Schema Registry supports Avro but has different API semantics |
| **MinIO** (S3 object storage) | Google Cloud Storage (GCS) | Amazon S3 | Used for Iceberg data lake storage AND media file uploads (`media-uploads` bucket) |
| **Project Nessie** (Iceberg catalog) | Self-hosted on GKE, or BigLake Metastore | AWS Glue Data Catalog, or self-hosted on EKS | Nessie currently runs `IN_MEMORY` — needs persistent backend |
| **ClickHouse** (OLAP warehouse) | ClickHouse Cloud | ClickHouse Cloud | Cloud-agnostic SaaS; connect via native protocol over TLS |
| **Prometheus + Loki + Jaeger + Grafana** | Google Cloud Operations Suite, or Grafana Cloud | Amazon CloudWatch + X-Ray, or Grafana Cloud | See Section 8 for migration path |
| **Supabase** (Postgres + Auth + Realtime) | Supabase Cloud (**AWS-only**) | Supabase Cloud (co-located) | See Section 10 for cross-cloud implications |
| **Kubernetes** (OrbStack) | GKE (Autopilot or Standard) | EKS (Managed Node Groups or Fargate) | — |

### Current Files Defining Local Infrastructure
- `docker-compose.yml` — all 11 services
- `redpanda-bootstrap.yaml` — Redpanda Iceberg + cloud storage config
- `clickhouse/init.sql` — ClickHouse table definitions (Kafka broker list hardcoded to `redpanda:29092`)

---

## 2. Secrets Management

### Hardcoded Credentials Inventory

Every credential below is currently stored in plaintext and checked into version control.

| Secret | Current Value | File(s) | Line(s) |
|---|---|---|---|
| MinIO root user/password | `admin` / `password` | `docker-compose.yml` | 57-58 |
| MinIO init alias | `admin password` | `docker-compose.yml` | 68 |
| Nessie S3 access key/secret | `admin` / `password` | `docker-compose.yml` | 79-80 |
| Redpanda cloud storage keys | `admin` / `password` | `redpanda-bootstrap.yaml` | 2-3 |
| Iceberg catalog dummy creds | `dummy` / `dummy` | `redpanda-bootstrap.yaml` | 15-16 |
| Supabase DB user/password | `postgres` / `postgres` | `flink_jobs/sql_runner.py` | 12-13 |
| Supabase DB user/password | `postgres` / `postgres` | `kubernetes/flink-deployment.yaml` | 47-49 |
| Supabase DB connection string | `postgres:postgres` | `kubernetes/go-services/message-consumer.yaml` | 26 |
| Supabase DB connection string | `postgres:postgres` | `go-services/message-consumer/main.go` | 55 |
| Webhook secret | `super-secret-webhook-key` | `kubernetes/go-services/api-gateway.yaml` | 28 |
| Webhook secret | `super-secret-webhook-key` | `supabase/migrations/20260221000000_login_webhook.sql` | 15 |
| Webhook secret | `super-secret-webhook-key` | `supabase/migrations/20260221000002_signup_webhook.sql` | 15 |
| Webhook secret | `super-secret-webhook-key` | `supabase/migrations/20260222000005_signout_webhook.sql` | 16 |
| Webhook secret | `super-secret-webhook-key` | `supabase/seed.sql` | 5-9 |
| Grafana anonymous admin | `GF_AUTH_ANONYMOUS_ORG_ROLE=Admin` | `docker-compose.yml` | 146 |
| SASL superuser password | `superuser-local-pw` | `scripts/setup-redpanda-auth*.sh`, `Taskfile.yml` | — |
| SASL api-gateway password | `api-gateway-local-pw` | `kubernetes/go-services/api-gateway.yaml` | — |
| SASL message-consumer password | `message-consumer-local-pw` | `kubernetes/go-services/message-consumer.yaml` | — |
| SASL flink-processor password | `flink-processor-local-pw` | `kubernetes/flink-deployment.yaml`, `kubernetes/pyflink-deployment.yaml` | — |
| SASL schema-admin password | `schema-admin-local-pw` | `docker-compose.yml` (Schema Registry JAAS) | — |
| SASL clickhouse-consumer password | `clickhouse-local-pw` | `clickhouse/kafka-sasl.xml` | — |
| MinIO access key (media-service) | `admin` | `kubernetes/go-services/media-service.yaml` | — |
| MinIO secret key (media-service) | `password` | `kubernetes/go-services/media-service.yaml` | — |
| MinIO webhook auth token | (if configured) | `scripts/setup-minio-webhook.sh` | — |

### Migration Path

| | GCP | AWS |
|---|---|---|
| **Secret Store** | GCP Secret Manager | AWS Secrets Manager |
| **K8s Integration** | External Secrets Operator (ESO) with GCP provider | External Secrets Operator (ESO) with AWS provider |
| **Pod Identity** | Workload Identity (no JSON key files) | IRSA (IAM Roles for Service Accounts) |

**Steps:**
1. Install External Secrets Operator in the cluster
2. Create `SecretStore` resource pointing to the cloud secret manager
3. Create `ExternalSecret` resources for each credential
4. Replace all hardcoded `value:` fields in K8s manifests with `valueFrom.secretKeyRef`
5. Remove all plaintext credentials from version control
6. Rotate every credential that was ever committed

---

## 3. Endpoint & DNS Hardcoding Audit

Every `host.docker.internal` and `localhost` reference must become an environment variable or K8s service DNS name.

### Full Inventory

| File | Line(s) | Current Value | Production Value |
|---|---|---|---|
| `go-services/api-gateway/main.go` | 284 | `host.docker.internal:9092` | `$REDPANDA_BROKERS` (env var, already reads it) |
| `go-services/api-gateway/main.go` | 290 | `http://host.docker.internal:8081` | `$SCHEMA_REGISTRY_URL` (env var, already reads it) |
| `go-services/message-consumer/main.go` | 53 | `localhost:9092` | Viper default; override via env |
| `go-services/message-consumer/main.go` | 54 | `http://localhost:8081` | Viper default; override via env |
| `go-services/message-consumer/main.go` | 55 | `postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable` | Viper default; override via env |
| `flink_jobs/sql_runner.py` | 9 | `host.docker.internal:9092` | ENV_DEFAULTS fallback; override via env |
| `flink_jobs/sql_runner.py` | 10 | `http://host.docker.internal:8081` | ENV_DEFAULTS fallback; override via env |
| `flink_jobs/sql_runner.py` | 11 | `jdbc:postgresql://host.docker.internal:54322/postgres` | ENV_DEFAULTS fallback; override via env |
| `kubernetes/go-services/api-gateway.yaml` | 24 | `host.docker.internal:9092` | Cloud broker endpoint |
| `kubernetes/go-services/api-gateway.yaml` | 26 | `http://host.docker.internal:8081` | Cloud schema registry URL |
| `kubernetes/go-services/message-consumer.yaml` | 22 | `host.docker.internal:9092` | Cloud broker endpoint |
| `kubernetes/go-services/message-consumer.yaml` | 24 | `http://host.docker.internal:8081` | Cloud schema registry URL |
| `kubernetes/go-services/message-consumer.yaml` | 26 | `...@host.docker.internal:54322/...?sslmode=disable` | Cloud DB URL with `sslmode=require` |
| `kubernetes/go-services/message-consumer-keda.yaml` | 14 | `host.docker.internal:9092` | Cloud broker endpoint |
| `kubernetes/flink-deployment.yaml` | 13 | `host.docker.internal` (StatsD host) | OTel Collector service DNS |
| `kubernetes/flink-deployment.yaml` | 41 | `host.docker.internal:9092` | Cloud broker endpoint |
| `kubernetes/flink-deployment.yaml` | 43 | `http://host.docker.internal:8081` | Cloud schema registry URL |
| `kubernetes/flink-deployment.yaml` | 45 | `jdbc:postgresql://host.docker.internal:54322/postgres` | Cloud DB JDBC URL |
| `kubernetes/flink-deployment.yaml` | 51 | `host.docker.internal` (OTEL_COLLECTOR_HOST) | OTel Collector service DNS |
| `kubernetes/pyflink-deployment.yaml` | 13 | `host.docker.internal` (StatsD host) | OTel Collector service DNS |
| `kubernetes/pyflink-deployment.yaml` | 41 | `host.docker.internal:9092` | Cloud broker endpoint |
| `kubernetes/pyflink-deployment.yaml` | 43 | `http://host.docker.internal:8081` | Cloud schema registry URL |
| `kubernetes/pyflink-deployment.yaml` | 45 | `host.docker.internal` (OTEL_COLLECTOR_HOST) | OTel Collector service DNS |
| `docker-compose.yml` | 14 | `host.docker.internal:9092` (Redpanda advertise addr) | N/A (Docker Compose not used in prod) |
| `clickhouse/init.sql` | 29 | `redpanda:29092` (Kafka broker list) | Cloud broker endpoint |
| `clickhouse/init.sql` | 33 | `http://schema-registry:8081` | Cloud schema registry URL |
| `telemetry/loki-config.yaml` | 8 | `127.0.0.1` (ingester address) | N/A if using managed Loki |

---

## 4. Network & TLS

### TLS Violations to Fix

| File | Line(s) | Issue |
|---|---|---|
| `go-services/message-consumer/main.go` | 55 | `sslmode=disable` in Postgres connection string |
| `kubernetes/go-services/message-consumer.yaml` | 26 | `sslmode=disable` in Postgres connection string |
| `redpanda-bootstrap.yaml` | 8 | `cloud_storage_disable_tls: true` |
| `telemetry/otel-collector-config.yaml` | 20-21 | `tls: insecure: true` on Prometheus remote write |
| `telemetry/otel-collector-config.yaml` | 30-31 | `tls: insecure: true` on Jaeger exporter |
| `telemetry/loki-config.yaml` | 1 | `auth_enabled: false` |

### Production Requirements

| | GCP | AWS |
|---|---|---|
| **Ingress Controller** | GKE Gateway API or Istio Ingress Gateway | AWS Load Balancer Controller (ALB) |
| **TLS Termination** | Google-managed TLS certificates | AWS Certificate Manager (ACM) |
| **WAF / DDoS** | Cloud Armor | AWS WAF + AWS Shield |
| **Internal mTLS** | Istio or Anthos Service Mesh | App Mesh or Istio |
| **DNS** | Cloud DNS | Route 53 |

### CORS Lockdown
`go-services/api-gateway/main.go:267` currently sets `Access-Control-Allow-Origin: *`. This must be restricted to the production frontend domain(s).

### MinIO/GCS CORS for Browser-Direct Uploads
The `media-uploads` MinIO bucket has permissive CORS (`*`) configured by `scripts/minio-init.sh`. In production, the GCS bucket CORS policy must restrict `AllowedOrigins` to the production frontend domain(s) and limit `AllowedMethods` to `PUT` only.

---

## 5. Kubernetes Hardening

### Missing Resource Limits

No deployment currently defines `resources.requests` or `resources.limits`:

| Deployment | File |
|---|---|
| `api-gateway` | `kubernetes/go-services/api-gateway.yaml` |
| `message-consumer` | `kubernetes/go-services/message-consumer.yaml` |

Flink deployments define JobManager/TaskManager resources at the FlinkDeployment level but lack pod-level container limits.

**Action:** Add resource blocks to every container spec:
```yaml
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi
```

### Missing Health Checks

Neither Go microservice exposes a health endpoint. Both K8s deployments lack liveness and readiness probes.

**Action:**
1. Add `/healthz` and `/readyz` HTTP endpoints to `api-gateway` and `message-consumer`
2. Add probe definitions to K8s manifests:
```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 5
```

### Missing Security Contexts

**Action:** Add to every pod spec:
```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 1000
  readOnlyRootFilesystem: true
```

### Image Pull Policy

All deployments use `imagePullPolicy: IfNotPresent` with `latest` tags:
- `kubernetes/go-services/api-gateway.yaml:19`
- `kubernetes/go-services/message-consumer.yaml:19`
- `kubernetes/flink-deployment.yaml:8`
- `kubernetes/pyflink-deployment.yaml:8`

**Action:** Use SHA-pinned image tags and `imagePullPolicy: Always`.

### Pod Disruption Budgets

**Action:** Add PDBs for all stateless services to ensure zero-downtime during node drains:
```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
spec:
  minAvailable: 1
```

---

## 6. High Availability & Scaling

### Current Single-Replica Deployments

| Deployment | File | Current Replicas |
|---|---|---|
| `api-gateway` | `kubernetes/go-services/api-gateway.yaml:7` | 1 |
| `message-consumer` | `kubernetes/go-services/message-consumer.yaml:7` | 1 |
| Flink SQL processor | `kubernetes/flink-deployment.yaml:67` | parallelism: 1 |
| PyFlink echo processor | `kubernetes/pyflink-deployment.yaml:63` | parallelism: 1 |

**Action:**
- `api-gateway`: Minimum 2 replicas + HPA based on CPU/request rate
- `message-consumer`: KEDA already handles scaling (min 1 → max 15), but validate threshold tuning for production traffic patterns
- Flink: Increase `parallelism` to match expected topic partition count

### Flink State & Upgrade Mode

Both Flink deployments use `upgradeMode: stateless` (`flink-deployment.yaml:68`, `pyflink-deployment.yaml:64`). This means all in-flight data is **lost** during upgrades.

**Action:** Switch to `upgradeMode: savepoint` and configure checkpointing:
```yaml
flinkConfiguration:
  state.checkpoints.dir: s3://bucket/flink/checkpoints
  state.savepoints.dir: s3://bucket/flink/savepoints
  execution.checkpointing.interval: 60000
  execution.checkpointing.min-pause: 30000
```

### Database Connection Pooling

The `message-consumer` opens a raw `sql.Open()` connection with no pool configuration (`main.go:86`). Under KEDA auto-scaling, 15 replicas with default Go pool settings can exhaust Supabase connection limits.

**Action:** Configure `db.SetMaxOpenConns()`, `db.SetMaxIdleConns()`, and `db.SetConnMaxLifetime()`. Alternatively, route through Supabase's built-in PgBouncer connection pooler (port 6543).

---

## 7. Application-Level Hardening

### API Gateway (`go-services/api-gateway/main.go`)

| Gap | Details | Priority |
|---|---|---|
| **No rate limiting** | Any client can send unlimited requests | CRITICAL |
| **Wildcard CORS** | Line 267: `Access-Control-Allow-Origin: *` | HIGH |
| **No health endpoints** | No `/healthz` or `/readyz` routes | HIGH |
| **Unstructured logging** | Uses `log.Printf` — no JSON, no levels, no correlation IDs | MEDIUM |
| **5s shutdown timeout** | Line 346: may be too short for in-flight Kafka produce callbacks | MEDIUM |
| **Hardcoded 1MB body limit** | Line 129: should be configurable | LOW |
| **Presigned URL expiry** | Upload presigned URLs have a fixed expiry — should be configurable via env var | LOW |

### Message Consumer (`go-services/message-consumer/main.go`)

| Gap | Details | Priority |
|---|---|---|
| **Fatal crash on DB error** | Line 157: `log.Fatalf` kills the process on any INSERT failure | CRITICAL |
| **No dead-letter queue** | Failed messages are lost forever after crash | CRITICAL |
| **No health endpoints** | Not reachable for K8s probes | HIGH |
| **No graceful shutdown** | Only handles `os.Interrupt`; no drain period for in-flight records | HIGH |
| **Unstructured logging** | Uses `log.Printf` — no JSON, no levels | MEDIUM |
| **No connection pool config** | Default Go pool under KEDA scaling may exhaust DB connections | MEDIUM |

---

## 8. Observability in Production

### OTel Collector Exporter Migration

The local OTel Collector (`telemetry/otel-collector-config.yaml`) exports to Prometheus, Loki, and Jaeger. In production, swap the `exporters` block — **no application code changes required**.

| Signal | GCP Exporter | AWS Exporter | Vendor-Neutral |
|---|---|---|---|
| **Metrics** | `googlecloud` | `awsemfexporter` (CloudWatch) | `datadog`, Grafana Cloud |
| **Logs** | `googlecloud` | `awscloudwatchlogs` | `datadog`, Grafana Cloud |
| **Traces** | `googlecloud` (Cloud Trace) | `awsxray` | `datadog`, Grafana Cloud |

### Alerting Rules (Must-Have)

- Kafka consumer group lag exceeds threshold (per-topic)
- API Gateway error rate > 1% over 5 minutes
- API Gateway P99 latency > 500ms
- Flink checkpoint failure
- Database connection pool exhaustion
- Pod restart count > 3 in 10 minutes

### Grafana Authentication

Local dev uses anonymous admin access (`GF_AUTH_ANONYMOUS_ENABLED=true`, `GF_AUTH_DISABLE_LOGIN_FORM=true`). Production must enforce identity-based authentication.

**Recommended: IAM / SSO integration** — Grafana supports cloud-native identity providers via environment variables. No plugin installation or custom code needed.

| Provider | Mechanism | Key Environment Variables |
|----------|-----------|---------------------------|
| **GCP** | IAP (Identity-Aware Proxy) | `GF_AUTH_PROXY_ENABLED=true`, `GF_AUTH_PROXY_HEADER_NAME=X-Goog-Authenticated-User-Email`, `GF_AUTH_PROXY_HEADER_PROPERTY=email`, `GF_AUTH_PROXY_AUTO_SIGN_UP=true` |
| **AWS** | ALB + Cognito (OIDC) | `GF_AUTH_GENERIC_OAUTH_ENABLED=true`, `GF_AUTH_GENERIC_OAUTH_NAME=Cognito`, `GF_AUTH_GENERIC_OAUTH_CLIENT_ID=<cognito-client-id>`, `GF_AUTH_GENERIC_OAUTH_CLIENT_SECRET=<from-secrets-manager>`, `GF_AUTH_GENERIC_OAUTH_SCOPES=openid email profile`, `GF_AUTH_GENERIC_OAUTH_AUTH_URL=https://<pool>.auth.<region>.amazoncognito.com/oauth2/authorize`, `GF_AUTH_GENERIC_OAUTH_TOKEN_URL=https://<pool>.auth.<region>.amazoncognito.com/oauth2/token`, `GF_AUTH_GENERIC_OAUTH_API_URL=https://<pool>.auth.<region>.amazoncognito.com/oauth2/userInfo` |
| **Azure** | Entra ID (AAD) | `GF_AUTH_AZUREAD_ENABLED=true`, `GF_AUTH_AZUREAD_CLIENT_ID=<app-id>`, `GF_AUTH_AZUREAD_CLIENT_SECRET=<from-key-vault>`, `GF_AUTH_AZUREAD_TENANT_ID=<tenant-id>`, `GF_AUTH_AZUREAD_AUTH_URL=https://login.microsoftonline.com/<tenant>/oauth2/v2.0/authorize`, `GF_AUTH_AZUREAD_TOKEN_URL=https://login.microsoftonline.com/<tenant>/oauth2/v2.0/token` |

**Production steps:**
1. Disable anonymous access: `GF_AUTH_ANONYMOUS_ENABLED=false`, remove `GF_AUTH_DISABLE_LOGIN_FORM`
2. Set the provider env vars above (store secrets via ESO from Section 2)
3. Configure role mapping: `GF_AUTH_<PROVIDER>_ROLE_ATTRIBUTE_PATH` to map IdP groups → Grafana roles (Admin/Editor/Viewer)
4. Optionally enable `GF_AUTH_<PROVIDER>_ALLOWED_ORGANIZATIONS` or `ALLOWED_DOMAINS` to restrict access

**GCP IAP note:** IAP sits in front of the Grafana Ingress and handles all authentication. Grafana trusts the `X-Goog-Authenticated-User-Email` header — no OAuth flow needed inside Grafana itself. This is the simplest path if you're already on GKE.

### Telemetry Storage in Production

Local dev uses named Docker volumes (Prometheus TSDB, Jaeger Badger, Loki filesystem). Production requires durable, scalable backends.

| Service | Local (Docker Volume) | GCP Production | AWS Production |
|---------|----------------------|----------------|----------------|
| **Prometheus** | `prometheus_data`, 15-day TSDB retention | Google Managed Prometheus (GMP), or Thanos → GCS | Amazon Managed Prometheus (AMP), or Thanos → S3 |
| **Jaeger** | `jaeger_data`, Badger disk storage | Tempo → GCS, or Jaeger → Elasticsearch on GKE | Tempo → S3, or Jaeger → OpenSearch Service |
| **Loki** | `loki_data`, filesystem chunks | Loki → GCS (`storage_config.gcs`), or Google Cloud Logging | Loki → S3 (`storage_config.aws`), or CloudWatch Logs |
| **Grafana** | Ephemeral (anonymous admin, no persistence needed) | Grafana Cloud, or self-hosted with Cloud SQL backend + IAP auth | Grafana Cloud, or self-hosted with RDS backend + Cognito auth |

**Config swap paths (no application changes):**
- **Prometheus → Thanos:** Add Thanos sidecar to Prometheus pod, configure `--objstore.config` for GCS/S3 bucket. Remote-write continues to work unchanged.
- **Jaeger → Tempo:** Change OTel Collector exporter from `otlp` (Jaeger) to `otlp` (Tempo endpoint). Same OTLP protocol, different backend.
- **Loki → GCS/S3:** Update `storage_config` in `loki-config.yaml` from `filesystem` to `gcs` or `aws`. Schema and queries remain identical.

### Log Retention

Define retention policies for all telemetry data:
- Metrics: 90 days (high resolution), 1 year (downsampled)
- Logs: 30 days hot, 90 days cold storage
- Traces: 7 days hot, 30 days cold storage

---

## 9. CI/CD Pipeline

### Current Implementation

The CI/CD pipeline is implemented via GitHub Actions with GHCR (GitHub Container Registry) as the image registry.

| Component | Implementation |
|---|---|
| **Container Registry** | GHCR (`ghcr.io/<owner>/event-substrate/<service>:<sha>`) |
| **CI Workflow** | `.github/workflows/ci.yml` — path-filtered builds, tests, and image push |
| **Deploy Workflow** | `.github/workflows/deploy.yml` — skeleton for staging/production (Pulumi TBD) |
| **K8s Packaging** | Helm chart (`charts/event-substrate/`) for Go services + Spark apps |

### CI Workflow (`.github/workflows/ci.yml`)

Path-based change detection via `dorny/paths-filter` ensures only affected services are built and tested:

| Filter | Paths | Jobs Triggered |
|---|---|---|
| `api-gateway` | `go-services/api-gateway/**` | test-go, build-push-api-gateway |
| `message-consumer` | `go-services/message-consumer/**` | test-go, build-push-message-consumer |
| `media-service` | `go-services/media-service/**` | test-go, build-push-media-service |
| `spark-base` | `Dockerfile.spark.base`, `pyspark_apps/common/**`, `pyspark_apps/pyproject.toml` | test-spark, build-push-spark |
| `spark-identity` | `pyspark_apps/identity/**` | test-spark, build-push-spark |
| `pyflink` | `Dockerfile.pyflink`, `pyflink_jobs/**` | build-push-pyflink |
| `helm` | `charts/**` | lint-helm |

Image tags use the full git SHA (`ghcr.io/<owner>/event-substrate/<service>:<sha>`). Build+push jobs only run on pushes to `main` (not on PRs). Concurrency groups (`ci-${{ github.ref }}`) with `cancel-in-progress` prevent duplicate builds.

### Deploy Strategy (Model 4 — Hybrid)

The deploy workflow uses a batching strategy:
- CI builds run per-merge (parallel, path-filtered)
- Deploys batch via `concurrency` group — latest commit on `main` wins
- One manual approval gate covers all changes since last production deploy
- Images are tagged with git SHA and never rebuilt — promotion is "point to SHA"

Deploy jobs are currently commented out pending Pulumi IaC setup. See `docs/github_cicd_plan.md` for the full roadmap.

### Helm Chart (`charts/event-substrate/`)

All Go services and Spark SparkApplication CRDs are managed via a single Helm chart. Local dev uses `imagePullPolicy: Never` (OrbStack shares Docker daemon). Production should use `imagePullPolicy: Always` with SHA-pinned image tags from GHCR.

### Cloud Registry Alternatives

For production, GHCR can be replaced with cloud-native registries:

| | GCP | AWS |
|---|---|---|
| **Container Registry** | Artifact Registry | Amazon ECR |
| **K8s Deployment** | Cloud Deploy or ArgoCD | CodeDeploy or ArgoCD |

### Pipeline Stages (Future — Full E2E in CI)

1. **Lint & Test** — Go tests, Spark pytest, Helm lint, Avro schema compatibility check
2. **Build** — Docker images with SHA tags (not `latest`)
3. **Schema Registry CI** — `curl` compatibility check against Schema Registry before merge to prevent breaking Avro changes
4. **Deploy to Staging** — Full integration test against staging Kafka/Postgres
5. **Deploy to Production** — Rolling update with PDB enforcement
6. **Database Migrations** — Run `supabase db push` or apply migration SQL in CI before deploying new application code

### Flink Job Versioning

Flink SQL changes require careful rollout:
1. Deploy new ConfigMap with updated SQL
2. Trigger Flink savepoint on running job
3. Cancel old job
4. Start new job from savepoint
5. Verify consumer offsets and processing lag

---

## 10. Cloud-Specific Deployment Notes

### GCP Deployment

#### Kubernetes: GKE

| Option | Recommendation |
|---|---|
| **GKE Autopilot** | Recommended for most workloads. Google manages nodes, auto-scales, enforces security baselines. Pay-per-pod. |
| **GKE Standard** | Use if you need GPU nodes, specific machine types, or custom node configurations for Flink TaskManagers. |

#### Supabase Cross-Cloud Latency

Supabase Cloud is **AWS-only** (17 AWS regions). A GCP deployment requires cross-cloud networking:

- **Same-metro latency** (e.g., GCP `us-east1` ↔ AWS `us-east-1`, both in Virginia): ~1-5ms round-trip
- **Cross-region latency** (e.g., GCP `us-west1` ↔ AWS `us-east-1`): ~60-80ms round-trip

**Mitigation options:**
1. Co-locate GKE in the same metro as the Supabase AWS region (e.g., both in `us-east-1` / `us-east1`)
2. Use Supabase's connection pooler (PgBouncer on port 6543) to reduce connection overhead
3. Consider a VPN tunnel or interconnect for private, lower-latency routing

#### GCP-Native Alternative: Firestore (TODO)

Supabase is AWS-only, making GCP deployments inherently cross-cloud. The most natural GCP-native replacement is **Firestore** (Auth via Firebase Auth, Realtime via Firestore listeners, Postgres equivalent via Firestore document DB or Cloud SQL).

Two migration strategies under consideration:

1. **GCP-only branch (preferred):** Create a dedicated branch that replaces Supabase with Firestore + Firebase Auth end-to-end. Use the [Firebase Emulator Suite](https://firebase.google.com/docs/emulator-suite) locally in place of `supabase start`. This avoids abstraction overhead and lets each branch use its platform's native SDK idioms.

2. **Interface abstraction:** Define Go interfaces (e.g., `AuthProvider`, `RealtimeStore`, `NotificationWriter`) that abstract Supabase-specific calls, then swap implementations via configuration. Higher upfront cost, and the Realtime subscription model (Supabase Postgres CDC vs. Firestore snapshot listeners) is fundamentally different enough that a clean interface may leak abstractions.

Option 1 is recommended — it keeps each deployment path simple and idiomatic rather than introducing a lowest-common-denominator abstraction layer.

#### GCP-Specific Services

| Service | Purpose |
|---|---|
| **Workload Identity** | Bind K8s service accounts to GCP IAM — no JSON key files needed for GCS, Secret Manager, etc. |
| **Cloud Armor** | WAF + DDoS protection in front of GKE Ingress |
| **Cloud DNS** | Managed DNS for your domain |
| **VPC Service Controls** | Restrict API access to authorized networks |
| **Cloud NAT** | Egress for private GKE nodes to reach Supabase Cloud |

---

### AWS Deployment

#### Kubernetes: EKS

| Option | Recommendation |
|---|---|
| **EKS Managed Node Groups** | Recommended. AWS manages EC2 lifecycle, you choose instance types. Best for Flink workloads needing specific memory/CPU. |
| **EKS with Fargate** | Serverless pods. Good for stateless services (api-gateway, message-consumer). Not ideal for Flink due to resource constraints. |

#### Supabase Co-Location

Supabase Cloud runs on AWS. Deploying EKS in the **same AWS region** as your Supabase project eliminates cross-cloud latency entirely.

**Networking options:**
1. **VPC Peering** — Connect EKS VPC to Supabase's VPC (if Supabase offers PrivateLink on your plan)
2. **Public endpoint** — Use Supabase's public endpoint with `sslmode=require` (simplest; adequate for most workloads)
3. **AWS PrivateLink** — Available on Supabase Pro/Enterprise plans for zero-public-internet database access

#### AWS-Specific Services

| Service | Purpose |
|---|---|
| **IRSA** (IAM Roles for Service Accounts) | Bind K8s service accounts to AWS IAM roles — no long-lived access keys needed for S3, Secrets Manager, etc. |
| **AWS WAF + Shield** | WAF + DDoS protection in front of ALB |
| **Route 53** | Managed DNS |
| **VPC Endpoints** | Private access to S3, ECR, Secrets Manager without traversing the public internet |
| **NAT Gateway** | Egress for private EKS nodes |

---

## 11. Media Upload: MinIO → Cloud Storage Migration

### Presigned URL Migration (MinIO → GCS/S3)
The `upload_handler.go` uses `CreditChecker` and `URLSigner` interfaces. In production:
- **GCP:** Replace MinIO presigned URLs with [GCS V4 Signed URLs](https://cloud.google.com/storage/docs/access-control/signed-urls). Use Workload Identity — no JSON key files. The `URLSigner` interface abstracts this swap.
- **AWS:** Replace with [S3 Presigned URLs](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html). Use IRSA for credentials.

### Storage Event Bridge (MinIO Webhook → Cloud Pub/Sub)
MinIO fires S3 event notifications via webhook to the media-service (`POST /webhooks/media-upload` for `uploads/` prefix, `POST /webhooks/file-ready` for `files/` prefix). In production:
- **GCP:** Replace with [GCS Pub/Sub notifications](https://cloud.google.com/storage/docs/pubsub-notifications). A Cloud Function or small adapter service subscribes to the Pub/Sub topic and produces to Kafka (or the media-service webhook handlers listen on a Pub/Sub subscription instead of HTTP webhooks).
- **AWS:** Replace with [S3 Event Notifications → SNS/SQS](https://docs.aws.amazon.com/AmazonS3/latest/userguide/EventNotifications.html). Route to a Lambda or the media-service webhook handlers via SQS.

### New Environment Variables

| Env Var | Service | Purpose |
|---|---|---|
| `MINIO_ENDPOINT` | media-service | MinIO/S3/GCS endpoint for presigned URL generation and object operations |
| `MINIO_ACCESS_KEY` | media-service | Storage access key (use Workload Identity/IRSA in prod) |
| `MINIO_SECRET_KEY` | media-service | Storage secret key (use Workload Identity/IRSA in prod) |
| `MINIO_BUCKET` | media-service | Bucket name for media uploads (`media-uploads`) |
| `SUPABASE_DB_URL` | media-service | Postgres connection string for file metadata and ownership queries |
| `SUPABASE_JWT_SECRET` | api-gateway | Symmetric HS256 JWT signing secret (Supabase project JWT secret) |
| `SUPABASE_JWKS_URL` | api-gateway | JWKS endpoint for ES256 public key fetch (e.g., `https://<project>.supabase.co/auth/v1/.well-known/jwks.json`) |

### Media Upload CORS Bucket Policy
The MinIO `media-uploads` bucket uses permissive CORS for local dev. In production, lock down:
- `AllowedOrigins`: production frontend domain only
- `AllowedMethods`: `PUT` only
- `AllowedHeaders`: `Content-Type`, `Content-Length`
- `MaxAgeSeconds`: 3600

---

## 12. Production Readiness Checklist

### Pre-Launch (Blocking)

- [ ] All `host.docker.internal` / `localhost` references replaced with cloud endpoints (Section 3)
- [ ] All plaintext credentials migrated to cloud secret manager (Section 2)
- [ ] `sslmode=require` on all Postgres connections (Section 4)
- [ ] TLS enabled on all inter-service communication (Section 4)
- [ ] CORS restricted to production domain (Section 4)
- [ ] Resource limits set on all K8s deployments (Section 5)
- [ ] Health check endpoints implemented in Go services (Section 5)
- [ ] Liveness/readiness probes added to all K8s deployments (Section 5)
- [ ] `log.Fatalf` in message-consumer replaced with retry + DLQ (Section 7)
- [ ] Rate limiting enabled on API Gateway (Section 7)
- [ ] CI/CD pipeline deploying SHA-tagged images (Section 9)
- [ ] Database migrations run in CI (Section 9)
- [ ] MinIO presigned URLs replaced with GCS/S3 signed URLs (Section 11)
- [ ] MinIO webhook replaced with GCS Pub/Sub or S3 Event Notifications (Section 11)
- [ ] Media upload bucket CORS restricted to production domain (Section 11)
- [ ] Storage credentials use Workload Identity / IRSA, not static keys (Section 11)

### Pre-Launch (Recommended)

- [ ] Structured JSON logging in all Go services (Section 7)
- [ ] Flink checkpointing + savepoint upgrade mode (Section 6)
- [ ] Database connection pooling configured (Section 6)
- [ ] Pod disruption budgets for all stateless services (Section 5)
- [ ] Security contexts (non-root) on all pods (Section 5)
- [ ] Alerting rules configured (Section 8)
- [ ] Grafana anonymous access disabled, IAM/SSO authentication enabled (Section 8)
- [ ] Telemetry storage migrated to durable backends — GCS/S3 for Prometheus, Loki, Jaeger (Section 8)
- [ ] Load testing performed against staging environment
- [ ] Disaster recovery plan documented (backup/restore for Postgres, Kafka offsets, Flink savepoints)

### Post-Launch

- [ ] Secret rotation schedule established
- [ ] Log retention policies enforced
- [ ] Cost monitoring and budget alerts configured
- [ ] Runbook created for common operational scenarios (scaling, failover, rollback)
