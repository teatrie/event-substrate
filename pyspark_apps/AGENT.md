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

```
pyspark_apps/
├── common/
│   └── session.py         # create_spark_session() with Iceberg+OpenLineage config
├── identity/
│   └── daily_login_aggregates/
│       ├── app.py         # build_output_df(), transform(), main()
│       └── tests/
│           ├── fixtures/  # JSON test data (login, signup, expected)
│           └── test_transforms.py
└── pyproject.toml
```

## Adding a New Job

1. Create `{domain}/{job_name}/app.py` with `build_output_df()`, `transform()`, `main()`
2. Create `{domain}/{job_name}/tests/` with fixtures and `test_transforms.py`
3. Add `{domain}` to `testpaths` in `pyproject.toml` if not already there
