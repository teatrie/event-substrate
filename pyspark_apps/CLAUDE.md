# Spark Jobs Agent Guide

## Running Tests Locally

PySpark requires Java. Set JAVA_HOME before running pytest:

```bash
export JAVA_HOME="/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home"
cd pyspark_apps
pip install -e '.[test]'
pytest identity/ -v
```

Or in one line:

```bash
JAVA_HOME="/opt/homebrew/opt/openjdk@21/libexec/openjdk.jdk/Contents/Home" pytest identity/ -v
```

## Structure

```text
pyspark_apps/
├── common/
│   └── session.py         # create_spark_session() with Iceberg+OpenLineage config
├── identity/
│   └── daily_login_aggregates/
│       ├── Dockerfile     # Thin production layer (FROM spark-base)
│       ├── app.py         # build_output_df(), transform(), main()
│       └── tests/
│           ├── fixtures/  # JSON test data (login, signup, expected)
│           └── test_transforms.py
└── pyproject.toml
```

## Build Architecture

Spark images use a **base + per-app** split:

- `Dockerfile.spark.base` (repo root) — Spark 4.0.2 + JARs + common/ (shared across all apps)
- `pyspark_apps/{domain}/{job_name}/Dockerfile` — thin layer FROM spark-base with app code only

```bash
# Build base (only when JARs, common/, or deps change)
task spark:build:base

# Build a specific app
task spark:build:identity

# Build all Spark images
task spark:build

# Run tests in Docker
task spark:test:docker
```

## Adding a New Job

1. Create `{domain}/{job_name}/app.py` with `build_output_df()`, `transform()`, `main()`
2. Create `{domain}/{job_name}/tests/` with fixtures and `test_transforms.py`
3. Create `{domain}/{job_name}/Dockerfile` (copy from `identity/daily_login_aggregates/Dockerfile`, update COPY path)
4. Add `{domain}` to `testpaths` in `pyproject.toml` if not already there
5. Add `spark:build:{name}` task in `Taskfile.yml` and wire into `spark:build` deps
6. Add SparkApplication template in `charts/event-substrate/templates/spark/`
7. Add image values in `charts/event-substrate/values.yaml` under `spark:`
8. Add path filter in `.github/workflows/ci.yml` for the new app

## Linting

Uses `ruff` for linting and `mypy` for type checking. Config in root `pyproject.toml`. Rules: E, W, F, I (imports), S (security), UP (pyupgrade). Line length 120. Run: `task lint:python` and `task check:types`. Auto-fix: `task lint:fix`.
