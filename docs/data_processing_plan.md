# Data Processing Plan: Spark & Airflow on Kubernetes

This document outlines the blueprint for integrating **Batch Processing** and **Workflow Orchestration** into the Event Substrate, complementing the existing real-time Flink pipeline.

---

## 1. Core Architecture: The Unified Compute Layer

To maintain the project's goal of **Multicloud Portability**, we will utilize a native Kubernetes-first approach for all processing.

### A. Batch Processing: Apache Spark on K8s ✅ IMPLEMENTED

* **Status**: Spark 4.0.2 with Kubeflow Spark Operator 2.1.0. First job: `identity/daily_login_aggregates` reads Iceberg login + signup tables, writes partitioned daily aggregates. Domain-organized code in `pyspark_apps/` with shared `common/session.py`. Multi-stage Dockerfile (production + test targets). OpenLineage Spark listener emits lineage to Marquez.
* **Engine**: **Spark on K8s Operator**.
* **Deployment**: Spark jobs are containerized and deployed as `SparkApplication` custom resources.
* **Why Operator?**:
  * **Portability**: Identical execution on OrbStack (Local), GKE (GCP), and EKS (AWS).
  * **Resource Efficiency**: Native K8s scheduling and executor scaling (ephemeral pods).
  * **Unity**: Shares the same VPC, IAM/Workload Identity, and Namespace as the Go microservices and Flink jobs.

### B. Orchestration: Apache Airflow ✅ IMPLEMENTED

* **Status**: Upgraded to Helm chart 1.19.0 (Airflow 3.1.7) with KubernetesExecutor, `hello_world` validation DAG + `identity/daily_login_aggregates` Spark trigger DAG. OpenLineage integration enabled (emits lineage to Marquez).
* **Deployment**: Self-managed via **Helm** (Official Apache Airflow chart 1.19.0) on K8s.
* **Executor**: **KubernetesExecutor**.
  * Each Airflow task runs in its own isolated pod.
  * Highly scalable and prevents a single heavy task from crashing the scheduler.
* **Local DAGs**: hostPath PV/PVC mounts `airflow/dags/` into scheduler and executor pods (OrbStack maps macOS at `/mnt/mac`).
* **Production DAGs**: git-sync sidecar polls `airflow/dags/` subpath (configured in `airflow/values-prod.yaml`).
* **Access**: `task airflow:ui` in a **separate terminal** → `http://localhost:8280` (admin/admin). This runs a blocking `kubectl port-forward` — keep the terminal open while using the UI. In production, an Ingress controller (nginx/Traefik) or LoadBalancer service replaces the port-forward.
* **Resources**: ~600m CPU, ~1.3Gi RAM total (fits alongside Flink on 8-core Mac).
* **Responsibility**:
  * Triggering Spark jobs for Daily/Weekly aggregation.
  * Managing Data Quality (DQ) checks.
  * Orchestrating Iceberg Table Maintenance (Metadata compaction, snapshot expiration).

---

## 2. The "Lambda-Lite" Architecture (Streaming + Batch)

The substrate uses **Apache Iceberg** as the bridge between Flink (Streaming) and Spark (Batch).

1. **Ingress & Stream (Fast Layer)**:
    * `api-gateway` -> `Redpanda` -> `Flink`.
    * Flink writes low-latency updates to Postgres (for the UI) and **Iceberg** (for the Lake).
2. **Batch & Analytics (Slow Layer)**:
    * **Spark** reads the Iceberg tables populated by Flink.
    * **Airflow** triggers Spark for complex join-heavy transformations, heavy deduplication, and historical backfills that are too expensive for Flink.
3. **Unified Query**:
    * **Trino** or **ClickHouse** provides a single SQL interface for both the Postgres operational data and the Iceberg analytical data.

---

## 3. Cloud Provider Mapping

While the core is portable via the Spark Operator, cloud-specific optimizations are available:

| Feature | AWS Implementation | GCP Implementation |
| :--- | :--- | :--- |
| **Managed K8s** | EKS | GKE |
| **Optimized Spark** | **EMR on EKS** (managed vertical scaling) | **Dataproc on GKE** (managed cluster tuning) |
| **Object Store** | S3 | GCS |
| **External Catalog** | AWS Glue Data Catalog | BigLake Metastore / Polaris |
| **Secret Store** | AWS Secrets Manager | GCP Secret Manager |

---

## 4. Development Workflow & Monorepo Integration

To stay within the "Developer-First" monorepo philosophy:

1. **Code Structure**:
    * `pyspark_apps/`: Domain-organized PySpark jobs (`identity/`, `media/`, `analytics/`) with shared `common/session.py`. Each job co-locates `app.py`, tests, and fixtures.
    * `airflow/dags/`: DAGs mirror `pyspark_apps/` domain structure (1:1 mapping). Airflow recursively scans subdirectories.
    * `Dockerfile.spark`: Multi-stage build — `production` (app only), `test` (adds pytest + fixtures).
2. **Building**:
    * `task spark:build` — builds production Spark image. `task test:spark` / `task spark:test:docker` — runs unit tests.
    * `task airflow:install` / `task airflow:upgrade` — deploys/upgrades Airflow Helm chart.
    * `task lineage:start` — starts Marquez lineage stack.
3. **Deployment**:
    * **Pulumi** will deploy the Airflow Helm chart and the Spark Operator.
    * GitHub Actions will use **Path-Based Filtering** (from the `github_cicd_plan.md`) to only deploy Spark/Airflow changes when those directories are touched.

---

## 5. Orchestration Deployment Strategy: Managed vs. Self-Managed

For the Event Substrate, we must choose between Cloud-Managed Airflow and Self-Managed Airflow on Kubernetes.

| Feature | Self-Managed (Helm on K8s) | Managed (MWAA / Cloud Composer) |
| :--- | :--- | :--- |
| **Recommendation** | **Strategic Choice for Substrate** | **Corporate/Ops-Heavy Choice** |
| **Cost** | Minimal (Runs on existing K8s nodes) | High ($300-$500/mo minimum) |
| **Portability** | High (Same Helm chart for AWS/GCP/Local) | Low (Cloud-specific APIs/UI) |
| **Local Dev** | Identical environment via OrbStack | No local parity (requires mocks) |
| **Maintenance** | Manual (Postgres/Scheduler tuning) | Automated (Google/AWS managed) |

**Decision:** The platform will utilize **Self-Managed Airflow on K8s** primarily to maintain **Multicloud Portability** and ensure that developers can test DAGs locally with 100% parity before pushing to production.

---

## 6. Implementation Status

1. ~~**Spark Operator POC**~~: ✅ Done — Kubeflow Spark Operator 2.1.0 installed. First SparkApplication: `identity/daily_login_aggregates` (Spark 4.0.2, Iceberg 1.10.1, PySpark). Domain-organized code in `pyspark_apps/`. Multi-stage Dockerfile with production + test targets.
2. ~~**Airflow Helm Setup**~~: ✅ Done — Upgraded to Helm chart 1.19.0 (Airflow 3.1.7) with KubernetesExecutor, hostPath DAG mount. `hello_world` validation DAG + `identity/daily_login_aggregates` Spark trigger DAG.
3. ~~**Data Lineage**~~: ✅ Done — Marquez 0.49.0 (API + Web UI + PostgreSQL) via docker-compose overlay. OpenLineage Spark listener (1.42.1) baked into Spark image. Airflow OpenLineage provider enabled via env vars. Both emit to Marquez.
4. **Static Lineage Skill (`/lineage`)**: Build an agent skill that derives data lineage statically — without requiring the Marquez runtime stack. Parse Spark `app.py` (table reads/writes from CLI args), Flink SQL (`INSERT INTO ... SELECT FROM`), Nessie REST catalog (`GET /iceberg/v1/namespaces/{ns}/tables`), and Redpanda Schema Registry (`GET /subjects`) to build a dependency graph. Complements Marquez: static analysis answers "what does this job read/write?" instantly at dev time; Marquez answers runtime questions (run status, row counts, data freshness, failure history).
5. **Unified Iceberg Access**: Ensure Spark, Flink, and Trino all share the same **Polaris/Nessie** catalog.
6. **Airflow Base Image**: Create a custom Airflow Docker image that includes the `kubectl` and `spark-submit` binaries.
7. **Governance Integration**: Ensure Spark jobs follow **Apache Ranger** policies for row/column level redaction.
