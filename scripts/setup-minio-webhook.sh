#!/bin/bash
# Configure MinIO bucket notifications to send webhook events to the API Gateway
# when files are uploaded to the media-uploads bucket.
#
# This replaces GCS Pub/Sub notifications in the local dev environment.
# In production, GCS → Pub/Sub → Cloud Function → Kafka.
#
# Prerequisites: MinIO must be running and media-uploads bucket must exist.
set -euo pipefail

MINIO_ALIAS="myminio"
MINIO_ENDPOINT="http://localhost:9000"
MINIO_USER="admin"
MINIO_PASS="password"
BUCKET="media-uploads"

# API Gateway webhook endpoint (accessible from within Docker network)
WEBHOOK_ENDPOINT="${GATEWAY_WEBHOOK_URL:-http://host.docker.internal:8080/webhooks/media-upload}"

echo "Configuring MinIO webhook notification for bucket '${BUCKET}'..."

# Set up mc alias
docker exec minio mc alias set ${MINIO_ALIAS} http://localhost:9000 ${MINIO_USER} ${MINIO_PASS} 2>/dev/null || true

# Configure webhook target on MinIO
# MinIO environment variables control webhook targets.
# For Docker Compose, these are set via environment variables on the minio service.
# Here we use mc admin config set for runtime configuration.
WEBHOOK_SECRET="${WEBHOOK_SECRET:-super-secret-webhook-key}"

docker exec minio mc admin config set ${MINIO_ALIAS} notify_webhook:gateway \
  endpoint="${WEBHOOK_ENDPOINT}" \
  auth_token="${WEBHOOK_SECRET}" \
  queue_dir="/tmp/minio-events" \
  queue_limit="10000" 2>/dev/null || {
    echo "Note: mc admin config set failed. MinIO webhook may need environment variable configuration."
    echo "Add to minio service in docker-compose.yml:"
    echo "  MINIO_NOTIFY_WEBHOOK_ENABLE_GATEWAY: 'on'"
    echo "  MINIO_NOTIFY_WEBHOOK_ENDPOINT_GATEWAY: '${WEBHOOK_ENDPOINT}'"
    echo "  MINIO_NOTIFY_WEBHOOK_AUTH_TOKEN_GATEWAY: '${WEBHOOK_SECRET}'"
}

# Restart MinIO to apply config (only needed for mc admin config set approach)
docker exec minio mc admin service restart ${MINIO_ALIAS} 2>/dev/null || true

# Wait for MinIO to come back
sleep 3

# Set up bucket event notification for ObjectCreated events
docker exec minio mc event add ${MINIO_ALIAS}/${BUCKET} arn:minio:sqs::GATEWAY:webhook \
  --event put \
  --suffix "" || {
    echo "Failed to add event notification. Trying alternative approach..."
    # Alternative: use MinIO client event add with just the ARN
    docker exec minio mc event add ${MINIO_ALIAS}/${BUCKET} arn:minio:sqs::GATEWAY:webhook --event put || true
}

echo "MinIO webhook notification configured."
echo "  Bucket: ${BUCKET}"
echo "  Events: s3:ObjectCreated:Put"
echo "  Target: ${WEBHOOK_ENDPOINT}"

# Verify bucket event notifications are registered
echo ""
echo "Verifying bucket event notifications..."
EVENTS=$(docker exec minio mc event list ${MINIO_ALIAS}/${BUCKET} 2>&1)
if [ -z "$EVENTS" ]; then
  echo "ERROR: No bucket event notifications registered!"
  exit 1
fi
echo "Bucket events registered:"
echo "$EVENTS"
