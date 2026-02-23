#!/usr/bin/env bash
# Runs from the HOST (uses docker exec). For manual re-runs and debugging.
# Creates SASL/SCRAM users via Admin API and ACLs via Kafka protocol.
set -euo pipefail

RPK="docker exec redpanda rpk"
SU_FLAGS="--user superuser --password superuser-local-pw --sasl-mechanism SCRAM-SHA-256"

echo "Waiting for Redpanda to be healthy..."
max_retries=30; count=0
while ! docker exec redpanda rpk cluster health 2>/dev/null | grep -q "Healthy:.*true"; do
  count=$((count + 1))
  if [ $count -gt $max_retries ]; then
    echo "Redpanda failed to become healthy" && exit 1
  fi
  sleep 2
done

echo "=== Creating SASL/SCRAM-SHA-256 Users ==="
$RPK acl user create superuser          --password 'superuser-local-pw'         --mechanism SCRAM-SHA-256
$RPK acl user create api-gateway         --password 'api-gateway-local-pw'       --mechanism SCRAM-SHA-256
$RPK acl user create flink-processor     --password 'flink-processor-local-pw'   --mechanism SCRAM-SHA-256
$RPK acl user create message-consumer    --password 'message-consumer-local-pw'  --mechanism SCRAM-SHA-256
$RPK acl user create schema-admin        --password 'schema-admin-local-pw'      --mechanism SCRAM-SHA-256
$RPK acl user create clickhouse-consumer --password 'clickhouse-local-pw'        --mechanism SCRAM-SHA-256

echo "=== Waiting for SCRAM credentials to propagate ==="
max_retries=15; count=0
until $RPK cluster info $SU_FLAGS 2>/dev/null; do
  count=$((count + 1))
  if [ $count -gt $max_retries ]; then
    echo "SASL auth not ready after retries" && exit 1
  fi
  sleep 2
done

echo "=== Setting Up ACLs ==="

# api-gateway: Write to public.* topics only
$RPK acl create --allow-principal User:api-gateway \
  --operation Write --operation Describe \
  --topic 'public.' --resource-pattern-type prefixed $SU_FLAGS

# flink-processor: Read public.*, Read/Write internal.*
$RPK acl create --allow-principal User:flink-processor \
  --operation Read --operation Describe \
  --topic 'public.' --resource-pattern-type prefixed $SU_FLAGS

$RPK acl create --allow-principal User:flink-processor \
  --operation Read --operation Write --operation Describe \
  --topic 'internal.' --resource-pattern-type prefixed $SU_FLAGS

$RPK acl create --allow-principal User:flink-processor \
  --operation Read \
  --group 'flink-' --resource-pattern-type prefixed $SU_FLAGS

$RPK acl create --allow-principal User:flink-processor \
  --operation Read \
  --group 'pyflink-' --resource-pattern-type prefixed $SU_FLAGS

# message-consumer: Read public.* only
$RPK acl create --allow-principal User:message-consumer \
  --operation Read --operation Describe \
  --topic 'public.' --resource-pattern-type prefixed $SU_FLAGS

$RPK acl create --allow-principal User:message-consumer \
  --operation Read \
  --group 'go-message-consumer' --resource-pattern-type literal $SU_FLAGS

# schema-admin: Schema Registry needs _schemas topic (Create + full CRUD)
$RPK acl create --allow-principal User:schema-admin \
  --operation Read --operation Write --operation Create --operation Describe --operation Alter \
  --topic '_schemas' --resource-pattern-type literal $SU_FLAGS

$RPK acl create --allow-principal User:schema-admin \
  --operation Describe \
  --topic '*' --resource-pattern-type literal $SU_FLAGS

$RPK acl create --allow-principal User:schema-admin \
  --operation Read \
  --group 'schema-registry' --resource-pattern-type prefixed $SU_FLAGS

# clickhouse-consumer: Read internal.* only
$RPK acl create --allow-principal User:clickhouse-consumer \
  --operation Read --operation Describe \
  --topic 'internal.' --resource-pattern-type prefixed $SU_FLAGS

$RPK acl create --allow-principal User:clickhouse-consumer \
  --operation Read \
  --group 'clickhouse-consumer' --resource-pattern-type literal $SU_FLAGS

echo "=== Redpanda Auth Setup Complete ==="
