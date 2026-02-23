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
| **MinIO** (S3 object storage) | Google Cloud Storage (GCS) | Amazon S3 | Used for Iceberg data lake storage |
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

### Log Retention

Define retention policies for all telemetry data:
- Metrics: 90 days (high resolution), 1 year (downsampled)
- Logs: 30 days hot, 90 days cold storage
- Traces: 7 days hot, 30 days cold storage

---

## 9. CI/CD Pipeline

| | GCP | AWS |
|---|---|---|
| **Container Registry** | Artifact Registry | Amazon ECR |
| **CI/CD** | Cloud Build or GitHub Actions | CodePipeline or GitHub Actions |
| **K8s Deployment** | Cloud Deploy or ArgoCD | CodeDeploy or ArgoCD |

### Pipeline Stages

1. **Lint & Test** — Go tests, SQL validation, Avro schema compatibility check
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

## 11. Production Readiness Checklist

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

### Pre-Launch (Recommended)

- [ ] Structured JSON logging in all Go services (Section 7)
- [ ] Flink checkpointing + savepoint upgrade mode (Section 6)
- [ ] Database connection pooling configured (Section 6)
- [ ] Pod disruption budgets for all stateless services (Section 5)
- [ ] Security contexts (non-root) on all pods (Section 5)
- [ ] Alerting rules configured (Section 8)
- [ ] Load testing performed against staging environment
- [ ] Disaster recovery plan documented (backup/restore for Postgres, Kafka offsets, Flink savepoints)

### Post-Launch

- [ ] Secret rotation schedule established
- [ ] Log retention policies enforced
- [ ] Cost monitoring and budget alerts configured
- [ ] Runbook created for common operational scenarios (scaling, failover, rollback)
