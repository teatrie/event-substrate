# Data Governance Plan: Event Substrate Lakehouse

## 1. Governance Objectives

The goal is to move from simple Infrastructure ACLs to comprehensive **Data Governance** that ensures:

* **Table-Level Access**: Control which services or users can see specific Iceberg tables.
* **Column-Level Security (CLS)**: Masking or hiding sensitive fields (PII, SSN) based on user roles.
* **Row-Level Security (RLS)**: Filtering data visibility (e.g., users only see data from their own `tenant_id`).
* **Data Lineage**: Tracking data flow from API Ingress -> Redpanda -> Flink -> Iceberg -> Spark -> Analytics.

---

## 2. Integrated Governance Architecture

We will implement a Three-Layer Governance model:

### Layer A: The Catalog (Table-Level ACLs)

The Catalog is the "Librarian" of the lakehouse.

* **Tool**: **Polaris Catalog** (Open Source) or **AWS Glue**.
* **Role**: Polaris provides a centralized RBAC layer for Iceberg tables. It ensures that whether a user connects via Trino, Spark, or Flink, they are authenticated against the same metadata store.

### Layer B: The Policy Engine (Fine-Grained Access)

* **Tool**: **Apache Ranger**.
* **Role**: Ranger acts as the "Bouncer." It integrates with SQL engines (Trino/Spark) to enforce:
  * **Column Masking**: Dynamic hashing/redaction of PII columns.
  * **Row Filtering**: Injecting `WHERE` clauses into queries automatically.
  * **Tag-Based Policies**: Policies applied to data based on tags (e.g., "PII", "Financial") rather than just table names.

### Layer C: The Compute layer (Enforcement)

* **SQL Ingress**: **Trino** (recommended as the unified query engine across Iceberg and ClickHouse).
* **Batch Processing**: **Apache Spark**.
* **Orchestration**: **Apache Airflow**.

---

## 3. Implementation with Spark & Airflow

As we integrate Spark and Airflow, they become critical enforcement points:

### A. Spark "Privacy-First" Jobs

* **Redaction DAGs**: Airflow will orchestrate Spark jobs that read "Raw" Iceberg tables (unmasked) and produce "Consumer-Ready" tables where row/column security is applied at the file level for lower-privilege users.
* **Data Contracts**: Use Spark to validate data quality and schema compliance against the Confluent Registry before committing to the production lake.

### B. Airflow Guardianship

* **Audit Logging**: Every DAG execution is logged, providing a clear lineage of who moved what data and where.
* **Schema Evolution Enforcement**: Airflow tasks can verify that a new deployment hasn't introduced breaking governance changes (e.g., a new PII column that isn't tagged).

---

## 4. Schema-Driven Governance (The "Gold" Standard)

Instead of manually configuring Ranger for every new table, we will use **Schema Tagging**:

1. **Tag in Avro**: Add metadata to the `.avsc` files in the `event-substrate` repo.

    ```json
    {
      "name": "email",
      "type": "string",
      "classification": "PII",
      "masking": "sha256"
    }
    ```

2. **Auto-Provision**: A custom CI/CD script reads these tags and automatically synchronizes them with **Apache Ranger** and **Polaris Catalog**.
3. **Automatic Protection**: The moment a new topic is materialized into the Iceberg lake, it is already "masked" for non-admin users.

---

## 5. Next Steps for Implementation

1. **Draft Polaris Deployment**: Add Polaris Catalog to the `deploy/` Pulumi infra.
2. **Integrate Trino**: Deploy Trino as the primary SQL gateway for the lake.
3. **PII Tagging**: Audit current `.avsc` files and add `classification` metadata.
4. **Airflow Sandbox**: Initialize the Airflow environment with a Spark-Iceberg connector.
