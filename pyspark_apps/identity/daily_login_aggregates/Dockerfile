# pyspark_apps/identity/daily_login_aggregates/Dockerfile
#
# Thin production layer for the daily-login-aggregates Spark job.
# Requires spark-base image to be built first:
#   docker build -f Dockerfile.spark.base --target base -t spark-base:latest .
#
# Build (from repo root):
#   docker build -f pyspark_apps/identity/daily_login_aggregates/Dockerfile \
#     -t pyspark/identity-daily-login-aggregates:latest .

ARG BASE_TAG=latest
FROM spark-base:${BASE_TAG}

# Copy only this app's domain code (common/ is already in the base image)
COPY pyspark_apps/identity/ /opt/spark/work-dir/pyspark_apps/identity/

# Run as the spark user (uid 185) per official image convention
USER 185
