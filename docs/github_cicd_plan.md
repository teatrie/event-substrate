# GitHub CI/CD Implementation Plan: Event Substrate

## 1. The Challenge: Resource Constraints
A standard GitHub-hosted runner (`ubuntu-latest`) provides **2 vCPUs and 7 GB of RAM**. 

The current local development stack, optimized for OrbStack/Apple Silicon, exceeds these limits during a full `task init` or `task test:e2e` run.

### Estimated Resource Footprint (Local Stack)
| Component | RAM Requirement | CPU Needs |
| :--- | :--- | :--- |
| **Flink (Job + Task Managers)** | ~2.0 GB | High (Java/Python) |
| **PyFlink Echo Job** | ~2.0 GB | High |
| **Redpanda** | 1.0 GB | Moderate |
| **Supabase Stack** | ~1.0 GB | Moderate |
| **ClickHouse** | ~0.5 GB | Low (idle) |
| **Kubernetes (Kind/k3s Overhead)** | ~1.0 GB | Moderate |
| **Telemetry (Prometheus/Loki/Grafana)** | ~0.7 GB | Low |
| **TOTAL** | **~8.2 GB** | **Exceeds 7GB Limit** |

---

## 2. Infrastructure Options & Account Requirements

To run this stack in GitHub, you have three primary infrastructure paths depending on your GitHub plan:

### A. Standard GitHub-Hosted Runners (GitHub Free/Pro)
- **Spec:** 2 vCPUs, 7 GB RAM.
- **Constraints:** Requires the **"Lean CI Profile"** (detailed below) to avoid OOM (Out of Memory) errors.
- **Cost:** Uses your monthly "Free Minutes" quota (2,000 mins for Free).

### B. Larger GitHub-Hosted Runners (GitHub Team/Enterprise)
- **Spec:** Options for 4, 8, or 16+ vCPUs and higher RAM.
- **Benefits:** Can run the full stack identically to local development without thinning.
- **Cost:** Pay-as-you-go (billed per minute, does not use free minutes).

### C. Self-Hosted Runners (All Plans)
- **Spec:** Hardware you provide (e.g., a dedicated Mac, Linux VPS, or on-prem server).
- **Benefits:** No cost for minutes; full control over resources. Ideal for 16GB+ RAM requirements typical of Flink/Redpanda stacks.
- **Setup:** Managed via **Settings > Actions > Runners** in the GitHub repo UI.

---

## 3. Proposed Strategy: "The Lean CI Profile"
To successfully run E2E tests in a standard CI environment without paying for "Larger Runners," we must parameterize the infrastructure.

### A. Docker Compose Profiles
We can use [Docker Compose Profiles](https://docs.docker.com/compose/profiles/) to disable "Human Observability" tools (Telemetry) during CI runs, as they are not required for functional verification.

**Implementation Plan:**
- Group `prometheus`, `loki`, `jaeger`, `grafana`, and `otel-collector` under the `telemetry` profile.
- Group `clickhouse` under an `analytics` profile if it's not strictly required for the core notification flow tests.

```yaml
# Example docker-compose.yml modification
services:
  grafana:
    profiles: ["telemetry"]
    image: grafana/grafana:latest
    # ...
```

### B. Environment Variable Parameterization
Parameterize memory and CPU limits in `docker-compose.yml` to allow the CI runner to "squeeze" services into the available memory.

```yaml
# Example Redpanda modification
services:
  redpanda:
    command:
      - redpanda
      - start
      - --memory ${CI_REDPANDA_MEM:-1G}
      - --smp ${CI_REDPANDA_CPU:-1}
```

---

## 3. CI/CD Workflow Stages
To prevent timeouts and CPU starvation, the CI should be split into discrete phases:

1. **Static Analysis & Linting:** (Go/JS/SQL) - Minimal resources.
2. **Infrastructure Initialization:**
   - Start "Lean" Docker Compose (No profiles).
   - Start Supabase.
   - Register Schemas and Create Topics.
3. **Control Plane Setup:**
   - Deploy Flink Operator.
   - Wait for pods to be ready (increased timeouts for 2-core environment).
4. **Execution:**
   - Run `task test:e2e`.

---

## 4. Proposed Taskfile Extensions
We will add a specific task to handle the "Lean" startup for CI runners.

```yaml
# Taskfile.yml preview
tasks:
  infra:ci:
    desc: Start a lean version of the infra for CI runners
    cmds:
      - docker compose up -d --build # No profiles = No telemetry
      - supabase start
```

## 5. Staging & Production Deployment Strategy

To move from local development to a production-grade system, the platform will utilize **GitHub Environments** and **Infrastructure as Code (IaC)**.

### A. Environment Promotion Flow
1. **Continuous Integration (CI):**
   - Triggered on PRs and merges to `main`.
   - Runs unit tests and "Lean CI" E2E tests.
   - Builds Docker images with **Short SHA tags**.
   - Pushes images to a private registry (GCP Artifact Registry or AWS ECR).
2. **Staging Deployment:**
   - Automatic deployment to a `staging` K8s namespace/cluster using Pulumi.
   - Runs full E2E validation against managed staging infrastructure.
3. **Production Deployment:**
   - Requires **Manual Approval** in the GitHub Actions UI.
   - Promotes the exact same Docker image SHA verified in staging.

### B. Infrastructure as Code (IaC): Pulumi (Go SDK)
Since the core microservices are written in Go, we will use the **Pulumi Go SDK** for infrastructure management.
- **Unified Language:** Share logic between Go services and infra (e.g., constants, routing logic).
- **Component Resources:** Create reusable blueprints for `FlinkJob` and `KafkaTopic`.
- **Industry Standard:** Pulumi is a tier-one, industry-accepted IaC tool used by organizations like Snowflake and Atlassian.

### C. Push vs. Pull (Pulumi vs. Flux/ArgoCD)
For the Event Substrate, we have chosen a **Push-based (Pulumi)** strategy over a **Pull-based (Flux/GitOps)** strategy for several technical reasons:

1. **Orchestration Complexity:** Event-driven systems have tight dependencies (e.g., Create Topic → Register Schema → Start Flink Job). Pulumi manages this dependency graph explicitly, whereas Pull-based tools often result in "crash-loop" cycles until all components eventually become consistent.
2. **Unified State:** Pulumi manages both Cloud Infrastructure (S3, RDS) and Kubernetes resources in a single pass. Flux/ArgoCD only manage Kubernetes, requiring a separate tool for the underlying infrastructure.
3. **Immediate Feedback:** With Pulumi in a GitHub Action, the pipeline fails immediately if a Flink job fails to boot. In a Pull-based system, the pipeline "succeeds" as soon as the YAML is committed, making deployment errors harder to trace.
4. **Developer Experience:** Using Pulumi (Go) allows developers to stay within their primary language and IDE, avoiding the overhead of managing specialized GitOps controllers inside the cluster.

### D. State Management & Costs
Pulumi offers flexible state management options:
1. **Pulumi Cloud (Managed):**
   - **Community Tier:** Free forever for individuals.
   - **Team Tier:** Usage-based (first 150k resource-hours/month are free). Provides a managed dashboard and manual approval gates.
2. **Self-Managed (S3/GCS Backend):**
   - **Cost:** $0 (strictly pay for storage).
   - **Sync & Locking:** Uses cloud provider native locking (e.g., S3 versioning/locking) to prevent race conditions during concurrent deployments.
   - **Privacy:** Infrastructure metadata never leaves your cloud environment.

### D. Custom Infrastructure Dashboard
As an alternative to the Pulumi Cloud UI, a custom-built dashboard can be integrated into the existing Vite frontend (`/admin` route):
- **Stack Visualization:** Automatically render architecture graphs from the Pulumi State JSON.
- **Observability Integration:** Layer real-time Flink and Redpanda metrics over the infrastructure map.
- **Local Control:** Each developer can run the dashboard locally, reading from the shared S3/GCS state bucket.
- **Deployment Status:** Real-time visibility into active locks and resource health.

### F. Multicloud Portability & Agent-Driven Adaptation
The "Event Substrate" is designed as a reusable chassis. While the core processing (Flink, Redpanda, K8s) is cloud-agnostic, the underlying infrastructure (Networking, IAM, Storage) varies by provider.

To support rapid project reuse, we will follow an **"Agent-Facilitated Translation"** model:
1. **Isolated Cloud Packages:** Cloud-specific Pulumi code will be kept in isolated directories (e.g., `deploy/infra/gcp` vs `deploy/infra/aws`). 
2. **Standardized High-Level Resources:** We will define high-level components (e.g., `SecureBucket`, `PrivateNetwork`) that wrap cloud-specific resources. 
3. **Agent-Led Migration:** When cloning the substrate for a new project on a different cloud provider:
   - An agent (like Antigravity) can read the existing GCP implementation.
   - Using its knowledge of both APIs, the agent can regenerate the AWS equivalents in a new branch, ensuring naming conventions and security policies are preserved.
   - The agent can then update the `go.mod` and CI/CD secrets to reflect the new provider.
4. **K8s as the Anchor:** By normalizing the compute layer in Kubernetes, we ensure that **90% of the platform (Flink jobs, Go services, and Kafka logic) never changes**, regardless of whether the cluster is GKE or EKS.

### G. Scaling for Large Teams: Blast Radius Management
As the team grows, the monorepo must be structured to prevent a single commit from breaking the entire platform.

1. **Path-Based CI Filtering:**
   - Use GitHub Actions path filtering (or tools like Nx/Turborepo) to ensure only the "impacted" services are built and tested.
   - Example: A change to `frontend/` should not trigger a full Flink re-deployment.
2. **Pulumi Micro-Stacks:**
   - Instead of one massive stack, split infrastructure into independent modules:
     - **Core Stack:** Networking, VPCs, IAM (Low frequency change).
     - **Data Stack:** Redpanda, ClickHouse, Postgres (Medium frequency).
     - **App Stack:** Go services and K8s manifests (High frequency).
   - This prevents accidental deletion of core stateful infrastructure during application updates.
3. **Contract-First Schema Enforcement:**
   - CI must enforce **Avro Schema Compatibility** checks against the Schema Registry before any merge.
   - If a schema change is "Backward Incompatible," the build fails, protecting existing consumers.
4. **Codeowners & Ownership Boundaries:**
   - Utilize `.github/CODEOWNERS` to automatically route reviews to domain experts (e.g., Flink experts for `flink_jobs/`).
5. **Feature Toggles:**
   - Implement "Dark Launches" where new logic is deployed to production but kept inactive via flags until verified.

---

## 8. Mobile Client Strategy & Schema Governance

As the platform expands to include mobile clients, strict separation and contract enforcement are required.

### A. Repository Isolation
- **iOS & Android Repos:** Separation into `ios-client` and `android-client` standalone repositories is recommended.
- **Benefits:** Prevents heavy mobile build tools (Xcode/Gradle) from slowing down backend CI/CD and allows for independent store release cycles.

### B. Registry-Driven Schema Syncing
Instead of manual SDK maintenance, mobile clients follow a **"Registry-Pull & Commit"** pattern:
1. **Source of Truth:** The Confluent Schema Registry serves as the central API gateway for data models.
2. **Versioned Synchronization:** Mobile repos contain a `schema_version.json` file to pin specifically verified schema versions.
3. **Local Code Generation:** Developers run a `task schemas:sync` script that fetches schemas from the registry and generates native Swift/Kotlin stubs.
4. **Git-Ops for Models:** Generated stubs are **committed** to the mobile repo to ensure offline support, IDE traceability, and a clear history of data model changes.

### C. Automated Compatibility Enforcement (Avro)
To protect the data pipeline, the backend CI enforces strict **Schema Evolution** rules:
- **Compatibility Modes:** Topics are configured for `BACKWARD` or `FULL` compatibility.
- **CI Barrier:** Any PR modifying a `.avsc` file triggers a `compatibility` check against the Registry API.
- **Breaking Change Detection:** Deleting non-default fields or changing data types will trigger a build failure, preventing breaking changes from reaching production consumers.

---

## 9. Implementation Status

| Item | Status | Notes |
|---|---|---|
| GitHub Actions CI workflow (`.github/workflows/ci.yml`) | **Done** | Path-filtered builds, tests, GHCR push |
| GitHub Actions deploy skeleton (`.github/workflows/deploy.yml`) | **Done** | Staging + production jobs commented out pending Pulumi |
| Helm chart (`charts/event-substrate/`) | **Done** | Go services + Spark apps, replaces raw kubectl manifests |
| Spark Docker split (base + per-app) | **Done** | `Dockerfile.spark.base` + per-app thin Dockerfiles |
| Taskfile Helm integration | **Done** | `helm:install`, `helm:template`, `helm:uninstall` tasks |
| GHCR image push | **Done** | `ghcr.io/<owner>/event-substrate/<service>:<sha>` |
| Lean CI profile (Docker Compose profiles) | Proposed | Not yet implemented — see Section 3 |
| Pulumi IaC (`deploy/` directory) | Roadmap | Pending cloud infra setup |
| Admin dashboard (Stack Visualizer) | Roadmap | Future frontend feature |
| Schema Registry CI compatibility check | Roadmap | `curl` check against registry before merge |
