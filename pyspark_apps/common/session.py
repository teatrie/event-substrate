import os

from pyspark.sql import SparkSession


def create_spark_session(app_name: str, catalog: str = "nessie") -> SparkSession:
    catalog_uri = os.environ.get("CATALOG_URI", "http://host.docker.internal:19120/iceberg")
    s3_endpoint = os.environ.get("S3_ENDPOINT", "http://host.docker.internal:9000")
    s3_access_key = os.environ.get("S3_ACCESS_KEY", "admin")
    s3_secret_key = os.environ.get("S3_SECRET_KEY", "password")
    openlineage_url = os.environ.get("OPENLINEAGE_URL", "http://host.docker.internal:5050")
    openlineage_namespace = os.environ.get("OPENLINEAGE_NAMESPACE", "spark")

    return (
        SparkSession.builder.appName(app_name)
        .config(f"spark.sql.catalog.{catalog}", "org.apache.iceberg.spark.SparkCatalog")
        .config(f"spark.sql.catalog.{catalog}.type", "rest")
        .config(f"spark.sql.catalog.{catalog}.uri", catalog_uri)
        .config(f"spark.sql.catalog.{catalog}.io-impl", "org.apache.iceberg.aws.s3.S3FileIO")
        .config(f"spark.sql.catalog.{catalog}.s3.endpoint", s3_endpoint)
        .config(f"spark.sql.catalog.{catalog}.s3.access-key-id", s3_access_key)
        .config(f"spark.sql.catalog.{catalog}.s3.secret-access-key", s3_secret_key)
        .config(f"spark.sql.catalog.{catalog}.s3.path-style-access", "true")
        .config("spark.hadoop.fs.s3a.endpoint", s3_endpoint)
        .config("spark.hadoop.fs.s3a.access.key", s3_access_key)
        .config("spark.hadoop.fs.s3a.secret.key", s3_secret_key)
        .config("spark.hadoop.fs.s3a.path.style.access", "true")
        .config("spark.extraListeners", "io.openlineage.spark.agent.OpenLineageSparkListener")
        .config("spark.openlineage.transport.type", "http")
        .config("spark.openlineage.transport.url", openlineage_url)
        .config("spark.openlineage.namespace", openlineage_namespace)
        .getOrCreate()
    )
