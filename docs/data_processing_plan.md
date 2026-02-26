# Data Processing Plan: Spark & Airflow on Kubernetes

This document outlines the blueprint for integrating **Batch Processing** and **Workflow Orchestration** into the Event Substrate, complementing the existing real-time Flink pipeline.

---

## 1. Core Architecture: The Unified Compute Layer

To maintain the project's goal of **Multicloud Portability**, we will utilize a native Kubernetes-first approach for all processing.

### A. Batch Processing: Apache Spark on K8s
*   **Engine**: **Spark on K8s Operator**.
*   **Deployment**: Spark jobs are containerized and deployed as `SparkApplication` custom resources.
*   **Why Operator?**: 
    *   **Portability**: Identical execution on OrbStack (Local), GKE (GCP), and EKS (AWS).
    *   **Resource Efficiency**: Native K8s scheduling and executor scaling (ephemeral pods).
    *   **Unity**: Shares the same VPC, IAM/Workload Identity, and Namespace as the Go microservices and Flink jobs.

### B. Orchestration: Apache Airflow
*   **Deployment**: Deploy via **Helm** (Official Apache Airflow chart) or **Astronomer Cosmos** for easier dbt integration.
*   **Executor**: **KubernetesExecutor**.
    *   Each Airflow task runs in its own isolated pod.
    *   Highly scalable and prevents a single heavy task from crashing the scheduler.
*   **Responsibility**:
    *   Triggering Spark jobs for Daily/Weekly aggregation.
    *   Managing Data Quality (DQ) checks.
    *   Orchestrating Iceberg Table Maintenance (Metadata compaction, snapshot expiration).

---

## 2. The "Lambda-Lite" Architecture (Streaming + Batch)

The substrate uses **Apache Iceberg** as the bridge between Flink (Streaming) and Spark (Batch).

1.  **Ingress & Stream (Fast Layer)**:
    *   `api-gateway` -> `Redpanda` -> `Flink`.
    *   Flink writes low-latency updates to Postgres (for the UI) and **Iceberg** (for the Lake).
2.  **Batch & Analytics (Slow Layer)**:
    *   **Spark** reads the Iceberg tables populated by Flink.
    *   **Airflow** triggers Spark for complex join-heavy transformations, heavy deduplication, and historical backfills that are too expensive for Flink.
3.  **Unified Query**:
    *   **Trino** or **ClickHouse** provides a single SQL interface for both the Postgres operational data and the Iceberg analytical data.

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

1.  **Code Structure**:
    *   `spark_jobs/`: Contains Spark Scala/Python code and `Dockerfile.spark`.
    *   `airflow/`: Contains DAG definitions, plugins, and requirements.
2.  **Building**: 
    *   `Taskfile.yml` will be updated with `task spark:build` and `task airflow:start`.
3.  **Deployment**:
    *   **Pulumi** will deploy the Airflow Helm chart and the Spark Operator.
    *   GitHub Actions will use **Path-Based Filtering** (from the `github_cicd_plan.md`) to only deploy Spark/Airflow changes when those directories are touched.

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

## 6. Next Steps for Implementation
1. **Spark Operator POC**: Install the Spark on K8s operator on the local OrbStack cluster.
2. **Airflow Helm Setup**: Define the Airflow Helm values in Pulumi for a `KubernetesExecutor` deployment.
3. **Unified Iceberg Access**: Ensure Spark, Flink, and Trino all share the same **Polaris/Nessie** catalog.
4. **Airflow Base Image**: Create a custom Airflow Docker image that includes the `kubectl` and `spark-submit` binaries.
5. **Governance Integration**: Ensure Spark jobs follow **Apache Ranger** policies for row/column level redaction.
