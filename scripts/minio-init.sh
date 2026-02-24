#!/bin/sh
# MinIO initialization: create buckets and configure media-uploads CORS.
set -e

sleep 5
mc alias set myminio http://minio:9000 admin password

# Redpanda tiered storage bucket
mc mb myminio/redpanda || true

# Media uploads bucket
mc mb myminio/media-uploads || true

# Set anonymous download policy so presigned URLs work for GET (future download feature)
mc anonymous set download myminio/media-uploads || true

# CORS policy for browser direct uploads via presigned PUT URLs
cat > /tmp/cors.xml <<'CORS'
<CORSConfiguration>
 <CORSRule>
   <AllowedOrigin>http://localhost:5173</AllowedOrigin>
   <AllowedOrigin>http://127.0.0.1:5173</AllowedOrigin>
   <AllowedMethod>PUT</AllowedMethod>
   <AllowedMethod>GET</AllowedMethod>
   <AllowedMethod>HEAD</AllowedMethod>
   <AllowedHeader>*</AllowedHeader>
   <ExposeHeader>ETag</ExposeHeader>
   <MaxAgeSeconds>3600</MaxAgeSeconds>
 </CORSRule>
</CORSConfiguration>
CORS

# Apply CORS policy via S3 PutBucketCors API
# mc doesn't support CORS natively, so use curl with S3 auth
CORS_MD5=$(cat /tmp/cors.xml | md5sum | awk '{print $1}' | xxd -r -p | base64)
DATE=$(date -R)
RESOURCE="/media-uploads/?cors"
SIG=$(printf "PUT\n${CORS_MD5}\napplication/xml\n${DATE}\n${RESOURCE}" | \
      openssl dgst -sha1 -hmac "password" -binary | base64)

curl -sf -X PUT "http://minio:9000/media-uploads/?cors" \
  -H "Date: ${DATE}" \
  -H "Content-MD5: ${CORS_MD5}" \
  -H "Content-Type: application/xml" \
  -H "Authorization: AWS admin:${SIG}" \
  -d @/tmp/cors.xml && echo "  ✅ CORS applied to media-uploads" || echo "  ⚠️  CORS config failed (non-fatal)"

echo "MinIO init complete: buckets [redpanda, media-uploads] created"
exit 0
