"""
daily_login_aggregates.py — Daily Spark job: aggregate login counts per user.

Schedule: 02:00 UTC daily.
Operator: KubernetesPodOperator (runs spark-submit in the Spark Docker image).
          All JARs are baked into the image; no external downloads at runtime.

Idempotent reruns: --dt {{ ds }} passed to the PySpark app so backfills
overwrite only the target partition for that date.
"""

from datetime import datetime

from airflow.providers.cncf.kubernetes.operators.pod import (
    KubernetesPodOperator,
)
from kubernetes.client.models import V1EnvVar

from airflow import DAG

_NAMESPACE = "spark-apps"
_IMAGE = "pyspark/identity-daily-login-aggregates:latest"

_ENV_VARS = [
    V1EnvVar(name="CATALOG_URI", value="http://host.docker.internal:19120/iceberg"),
    V1EnvVar(name="S3_ENDPOINT", value="http://host.docker.internal:9000"),
    V1EnvVar(name="S3_ACCESS_KEY", value="admin"),
    V1EnvVar(name="S3_SECRET_KEY", value="password"),
    V1EnvVar(name="OPENLINEAGE_URL", value="http://host.docker.internal:5050"),
    V1EnvVar(name="OPENLINEAGE_NAMESPACE", value="spark"),
]

with DAG(
    dag_id="identity__daily_login_aggregates",
    description="Aggregate daily login counts per user via Spark + Iceberg",
    start_date=datetime(2024, 1, 1),
    schedule="0 2 * * *",
    catchup=False,
    tags=["identity", "spark", "daily"],
) as dag:
    run_aggregates = KubernetesPodOperator(
        task_id="run_daily_login_aggregates",
        name="identity-login-agg-{{ ds_nodash }}",
        namespace=_NAMESPACE,
        image=_IMAGE,
        image_pull_policy="IfNotPresent",
        cmds=["/opt/spark/bin/spark-submit"],
        arguments=[
            "--master",
            "local[*]",
            "--conf",
            "spark.openlineage.transport.url=http://host.docker.internal:5050",
            "--conf",
            "spark.openlineage.namespace=spark",
            "--conf",
            "spark.openlineage.transport.type=http",
            "--conf",
            "spark.extraListeners=io.openlineage.spark.agent.OpenLineageSparkListener",
            "/opt/spark/work-dir/pyspark_apps/identity/daily_login_aggregates/app.py",
            "--catalog",
            "nessie",
            "--login-table",
            "nessie.redpanda.public_identity_login_events",
            "--signup-table",
            "nessie.redpanda.public_identity_signup_events",
            "--output-table",
            "nessie.silver.identity_daily_login_aggregates",
            "--dt",
            "{{ ds }}",
        ],
        env_vars=_ENV_VARS,
        is_delete_operator_pod=True,
        get_logs=True,
    )
