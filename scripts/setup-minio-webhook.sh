#!/bin/bash
# Configure MinIO bucket notifications to send webhook events to the Media Service
# when files appear in the uploads/ or files/ prefixes.
#
# uploads/ prefix → /webhooks/media-upload (new upload detected)
# files/   prefix → /webhooks/file-ready  (move-to-permanent completed)
#
# Webhook TARGETS are configured via environment variables in docker-compose.yml
# (MINIO_NOTIFY_WEBHOOK_ENABLE_*, MINIO_NOTIFY_WEBHOOK_ENDPOINT_*).
# This script only adds BUCKET EVENT NOTIFICATIONS that route events to those targets.
#
# Prerequisites: MinIO must be running and media-uploads bucket must exist.
set -euo pipefail

MINIO_ALIAS="myminio"
MINIO_USER="admin"
MINIO_PASS="password"
BUCKET="media-uploads"

echo "Configuring MinIO webhook notifications for bucket '${BUCKET}'..."

# Set up mc alias
docker exec minio mc alias set ${MINIO_ALIAS} http://localhost:9000 ${MINIO_USER} ${MINIO_PASS} 2>/dev/null || true

# Remove any existing event notifications to start clean
docker exec minio mc event remove ${MINIO_ALIAS}/${BUCKET} --force 2>/dev/null || true

# Set up bucket event notification for uploads/ prefix → media-upload handler
# The MEDIASERVICE_UPLOADS target is defined by env vars in docker-compose.yml
docker exec minio mc event add ${MINIO_ALIAS}/${BUCKET} arn:minio:sqs::MEDIASERVICE_UPLOADS:webhook \
  --event put \
  --prefix "uploads/" || {
    echo "Failed to add uploads/ event notification."
    echo "Ensure MINIO_NOTIFY_WEBHOOK_ENABLE_MEDIASERVICE_UPLOADS=on is set in docker-compose.yml"
}

# Set up bucket event notification for files/ prefix → file-ready handler
# The MEDIASERVICE_FILES target is defined by env vars in docker-compose.yml
docker exec minio mc event add ${MINIO_ALIAS}/${BUCKET} arn:minio:sqs::MEDIASERVICE_FILES:webhook \
  --event put \
  --prefix "files/" || {
    echo "Failed to add files/ event notification."
    echo "Ensure MINIO_NOTIFY_WEBHOOK_ENABLE_MEDIASERVICE_FILES=on is set in docker-compose.yml"
}

echo "MinIO webhook notifications configured."
echo "  Bucket: ${BUCKET}"
echo "  uploads/ → MEDIASERVICE_UPLOADS webhook target"
echo "  files/   → MEDIASERVICE_FILES webhook target"

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
