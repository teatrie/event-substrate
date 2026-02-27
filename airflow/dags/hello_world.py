"""
hello_world.py — Minimal Airflow DAG for validating KubernetesExecutor.

Triggers manually (no schedule). Two tasks:
  1. bash_hello  — BashOperator prints a greeting
  2. python_hello — PythonOperator returns platform info

If both tasks succeed, the KubernetesExecutor is working: each task
ran in its own K8s pod, was scheduled, executed, and cleaned up.
"""

from datetime import datetime

from airflow import DAG
from airflow.providers.standard.operators.bash import BashOperator
from airflow.providers.standard.operators.python import PythonOperator


def _python_hello():
    import platform

    info = {
        "message": "Hello from PythonOperator!",
        "python": platform.python_version(),
        "node": platform.node(),
        "system": platform.system(),
    }
    print(info)
    return info


with DAG(
    dag_id="hello_world",
    description="Skeleton DAG — validates KubernetesExecutor pod lifecycle",
    start_date=datetime(2024, 1, 1),
    schedule=None,  # manual trigger only
    catchup=False,
    tags=["skeleton", "validation"],
) as dag:

    bash_hello = BashOperator(
        task_id="bash_hello",
        bash_command='echo "Hello from BashOperator on $(hostname)!"',
    )

    python_hello = PythonOperator(
        task_id="python_hello",
        python_callable=_python_hello,
    )

    bash_hello >> python_hello
