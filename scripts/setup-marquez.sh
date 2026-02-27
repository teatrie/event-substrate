#!/usr/bin/env bash
set -euo pipefail

MARQUEZ_API="http://localhost:5050"
MAX_RETRIES=30
SLEEP_SECONDS=2

echo "Waiting for Marquez API to be ready..."
for i in $(seq 1 $MAX_RETRIES); do
  if curl -sf "${MARQUEZ_API}/api/v1/namespaces" -o /dev/null 2>&1; then
    echo "Marquez API is ready."
    break
  fi
  if [ "$i" -eq "$MAX_RETRIES" ]; then
    echo "ERROR: Marquez API did not become ready after $((MAX_RETRIES * SLEEP_SECONDS))s. Is it running?" >&2
    exit 1
  fi
  echo "  Attempt $i/$MAX_RETRIES — retrying in ${SLEEP_SECONDS}s..."
  sleep "$SLEEP_SECONDS"
done

echo "Creating 'spark' namespace..."
curl -sf -X PUT "${MARQUEZ_API}/api/v1/namespaces/spark" \
  -H 'Content-Type: application/json' \
  -d '{"ownerName": "event-substrate", "description": "Spark batch jobs"}' \
  | jq . || true

echo "Creating 'airflow' namespace..."
curl -sf -X PUT "${MARQUEZ_API}/api/v1/namespaces/airflow" \
  -H 'Content-Type: application/json' \
  -d '{"ownerName": "event-substrate", "description": "Airflow DAG tasks"}' \
  | jq . || true

echo ""
echo "Marquez setup complete."
echo "  API:    ${MARQUEZ_API}"
echo "  Web UI: http://localhost:3001"
