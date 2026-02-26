#!/usr/bin/env bash
# Runs INSIDE Docker network (used by redpanda-auth-init container).
# Creates SASL/SCRAM users via Admin API (unauthenticated) and ACLs via Kafka protocol.
set -euo pipefail

ADMIN_URL="redpanda:9644"
BROKERS="redpanda:29092"
SU_FLAGS="--user superuser --password superuser-local-pw --sasl-mechanism SCRAM-SHA-256"

echo "=== Creating SASL/SCRAM-SHA-256 Users ==="
rpk acl user create superuser         --password 'superuser-local-pw'         --mechanism SCRAM-SHA-256 --api-urls "$ADMIN_URL"
rpk acl user create api-gateway        --password 'api-gateway-local-pw'       --mechanism SCRAM-SHA-256 --api-urls "$ADMIN_URL"
rpk acl user create flink-processor    --password 'flink-processor-local-pw'   --mechanism SCRAM-SHA-256 --api-urls "$ADMIN_URL"
rpk acl user create message-consumer   --password 'message-consumer-local-pw'  --mechanism SCRAM-SHA-256 --api-urls "$ADMIN_URL"
rpk acl user create schema-admin       --password 'schema-admin-local-pw'      --mechanism SCRAM-SHA-256 --api-urls "$ADMIN_URL"
rpk acl user create clickhouse-consumer --password 'clickhouse-local-pw'       --mechanism SCRAM-SHA-256 --api-urls "$ADMIN_URL"
rpk acl user create media-service      --password 'media-service-local-pw'     --mechanism SCRAM-SHA-256 --api-urls "$ADMIN_URL"

echo "=== Waiting for SCRAM credentials to propagate ==="
max_retries=15; count=0
until rpk cluster info --brokers "$BROKERS" $SU_FLAGS 2>/dev/null; do
  count=$((count + 1))
  if [ $count -gt $max_retries ]; then
    echo "SASL auth not ready after retries" && exit 1
  fi
  sleep 2
done

echo "=== Setting Up ACLs ==="

# api-gateway: Write to public.* and internal.media.* topics (intent events)
rpk acl create --allow-principal User:api-gateway \
  --operation Write --operation Describe \
  --topic 'public.' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:api-gateway \
  --operation Write --operation Describe \
  --topic 'internal.media.' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

# flink-processor: Read/Write public.* (writes approved/rejected events), Read/Write internal.*
rpk acl create --allow-principal User:flink-processor \
  --operation Read --operation Write --operation Describe \
  --topic 'public.' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:flink-processor \
  --operation Read --operation Write --operation Describe \
  --topic 'internal.' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:flink-processor \
  --operation Read \
  --group 'flink-' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:flink-processor \
  --operation Read \
  --group 'pyflink-' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

# message-consumer: Read public.* only
rpk acl create --allow-principal User:message-consumer \
  --operation Read --operation Describe \
  --topic 'public.' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:message-consumer \
  --operation Read \
  --group 'go-message-consumer' --resource-pattern-type literal \
  --brokers "$BROKERS" $SU_FLAGS

# schema-admin: Schema Registry needs _schemas topic (Create + full CRUD)
rpk acl create --allow-principal User:schema-admin \
  --operation Read --operation Write --operation Create --operation Describe --operation Alter \
  --topic '_schemas' --resource-pattern-type literal \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:schema-admin \
  --operation Describe \
  --topic '*' --resource-pattern-type literal \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:schema-admin \
  --operation Read \
  --group 'schema-registry' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

# media-service: Read public.media.* and internal.media.*, Write public.media.*
rpk acl create --allow-principal User:media-service \
  --operation Read --operation Write --operation Describe \
  --topic 'public.media.' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:media-service \
  --operation Read --operation Write --operation Describe \
  --topic 'internal.media.' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:media-service \
  --operation Read \
  --group 'media-service-consumer' --resource-pattern-type literal \
  --brokers "$BROKERS" $SU_FLAGS

# clickhouse-consumer: Read internal.* only
rpk acl create --allow-principal User:clickhouse-consumer \
  --operation Read --operation Describe \
  --topic 'internal.' --resource-pattern-type prefixed \
  --brokers "$BROKERS" $SU_FLAGS

rpk acl create --allow-principal User:clickhouse-consumer \
  --operation Read \
  --group 'clickhouse-consumer' --resource-pattern-type literal \
  --brokers "$BROKERS" $SU_FLAGS

echo "=== Redpanda Auth Setup Complete ==="
