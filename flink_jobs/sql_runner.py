import sys
import os
import re
from pyflink.datastream import StreamExecutionEnvironment
from pyflink.table import StreamTableEnvironment, EnvironmentSettings

# Local dev defaults — overridden by environment variables in production
ENV_DEFAULTS = {
    'REDPANDA_BROKERS': 'host.docker.internal:9092',
    'SCHEMA_REGISTRY_URL': 'http://host.docker.internal:8081',
    'SUPABASE_DB_JDBC_URL': 'jdbc:postgresql://host.docker.internal:54322/postgres?tcpKeepAlive=true&connectTimeout=10&socketTimeout=30',
    'SUPABASE_DB_USER': 'postgres',
    'SUPABASE_DB_PASSWORD': 'postgres',
    'ICEBERG_REST_CATALOG_ENDPOINT': 'http://host.docker.internal:19120/iceberg',
    'S3_ENDPOINT': 'http://host.docker.internal:9000',
    'S3_ACCESS_KEY': 'admin',
    'S3_SECRET_KEY': 'password',
    'S3_BUCKET': 'redpanda',
    # SASL defaults for local dev
    'KAFKA_SECURITY_PROTOCOL': 'SASL_PLAINTEXT',
    'KAFKA_SASL_MECHANISM': 'SCRAM-SHA-256',
    'KAFKA_SASL_USERNAME': 'flink-processor',
    'KAFKA_SASL_PASSWORD': 'flink-processor-local-pw',
}

def env(var_name):
    """Get environment variable with fallback to local dev defaults."""
    return os.environ.get(var_name, ENV_DEFAULTS.get(var_name, ''))

def resolve_env(sql_content):
    """Replace ${VAR} placeholders with environment variables (falling back to local dev defaults)."""
    def replacer(match):
        var_name = match.group(1)
        return os.environ.get(var_name, ENV_DEFAULTS.get(var_name, match.group(0)))
    return re.sub(r'\$\{(\w+)\}', replacer, sql_content)

def register_iceberg_catalog(t_env):
    """Register Nessie-backed Iceberg REST catalog for querying tiered storage tables.
    Non-fatal: the core pipeline works without it (requires hadoop-common on classpath)."""
    try:
        t_env.execute_sql(f"""
            CREATE CATALOG iceberg_catalog WITH (
                'type' = 'iceberg',
                'catalog-type' = 'rest',
                'uri' = '{env('ICEBERG_REST_CATALOG_ENDPOINT')}',
                'warehouse' = 's3://{env('S3_BUCKET')}/',
                's3.endpoint' = '{env('S3_ENDPOINT')}',
                's3.access-key-id' = '{env('S3_ACCESS_KEY')}',
                's3.secret-access-key' = '{env('S3_SECRET_KEY')}',
                's3.path-style-access' = 'true'
            )
        """)
        print("Registered Iceberg catalog (Nessie REST)")
    except Exception as e:
        print(f"WARNING: Iceberg catalog registration failed (non-fatal): {e}")
        print("  Tiered storage queries will be unavailable. Core pipeline unaffected.")

def kafka_sasl_props():
    """Build SASL properties string for Kafka connectors. Empty string if not configured."""
    mechanism = env('KAFKA_SASL_MECHANISM')
    if not mechanism:
        return ''
    return f""",
                'properties.security.protocol' = '{env('KAFKA_SECURITY_PROTOCOL')}',
                'properties.sasl.mechanism' = '{mechanism}',
                'properties.sasl.jaas.config' = 'org.apache.flink.kafka.shaded.org.apache.kafka.common.security.scram.ScramLoginModule required username="{env('KAFKA_SASL_USERNAME')}" password="{env('KAFKA_SASL_PASSWORD')}";'"""

def register_kafka_sources(t_env):
    """Register all Kafka source tables centrally. SQL files only need INSERT logic."""
    brokers = env('REDPANDA_BROKERS')
    sr_url = env('SCHEMA_REGISTRY_URL')
    sasl_props = kafka_sasl_props()

    sources = {
        'login_events': {
            'topic': 'public.identity.login.events',
            'group_id': 'flink-login-consumer',
            'fields': '`user_id` STRING, `email` STRING, `login_time` STRING, `device_id` STRING, `user_agent` STRING, `ip_address` STRING',
        },
        'signup_events': {
            'topic': 'public.identity.signup.events',
            'group_id': 'flink-signup-consumer',
            'fields': '`user_id` STRING, `email` STRING, `signup_time` STRING',
        },
        'signout_events': {
            'topic': 'public.identity.signout.events',
            'group_id': 'flink-signout-consumer',
            'fields': '`user_id` STRING, `email` STRING, `signout_time` STRING, `device_id` STRING, `user_agent` STRING, `ip_address` STRING',
        },
        'media_upload_events': {
            'topic': 'public.media.upload.confirmed',
            'group_id': 'flink-media-confirmed-consumer',
            'fields': '`user_id` STRING, `email` STRING, `file_path` STRING, `file_name` STRING, `file_size` BIGINT, `media_type` STRING, `upload_time` STRING',
        },
        'media_expired_events': {
            'topic': 'public.media.expired.events',
            'group_id': 'flink-expired-consumer',
            'fields': '`user_id` STRING, `file_path` STRING, `file_name` STRING, `file_size` BIGINT, `media_type` STRING, `request_id` STRING, `request_time` STRING, `expired_time` STRING',
        },
        'upload_signed_events': {
            'topic': 'public.media.upload.signed',
            'group_id': 'flink-upload-signed-consumer',
            'fields': '`user_id` STRING, `file_path` STRING, `file_name` STRING, `upload_url` STRING, `expires_in` INT, `request_id` STRING, `signed_time` STRING',
        },
        'upload_rejected_events': {
            'topic': 'public.media.upload.rejected',
            'group_id': 'flink-upload-rejected-consumer',
            'fields': '`user_id` STRING, `file_name` STRING, `media_type` STRING, `file_size` BIGINT, `request_id` STRING, `reason` STRING, `rejected_time` STRING',
        },
        'download_signed_events': {
            'topic': 'public.media.download.signed',
            'group_id': 'flink-download-signed-consumer',
            'fields': '`user_id` STRING, `file_path` STRING, `download_url` STRING, `expires_in` INT, `request_id` STRING, `signed_time` STRING',
        },
        'download_rejected_events': {
            'topic': 'public.media.download.rejected',
            'group_id': 'flink-download-rejected-consumer',
            'fields': '`user_id` STRING, `file_path` STRING, `request_id` STRING, `reason` STRING, `rejected_time` STRING',
        },
        'media_delete_events': {
            'topic': 'public.media.delete.events',
            'group_id': 'flink-delete-consumer',
            'fields': '`user_id` STRING, `file_path` STRING, `file_name` STRING, `media_type` STRING, `file_size` BIGINT, `delete_time` STRING',
        },
        'delete_rejected_events': {
            'topic': 'public.media.delete.rejected',
            'group_id': 'flink-delete-rejected-consumer',
            'fields': '`user_id` STRING, `file_path` STRING, `request_id` STRING, `reason` STRING, `rejected_time` STRING',
        },
        'upload_dead_letter_events': {
            'topic': 'internal.media.upload.dead-letter',
            'group_id': 'flink-dead-letter-consumer',
            'fields': '`user_id` STRING, `email` STRING, `file_path` STRING, `permanent_path` STRING, `file_name` STRING, `file_size` BIGINT, `media_type` STRING, `upload_time` STRING, `retry_count` INT, `failure_reason` STRING, `dead_letter_time` STRING',
        },
    }

    # Register unified events Kafka sink (avoids JAAS semicolon issue with SQL file splitting)
    t_env.execute_sql(f"""
        CREATE TABLE platform_unified_events (
            `event_id` STRING,
            `event_time` STRING,
            `event_type` STRING,
            `user_id` STRING,
            `email` STRING,
            `device_id` STRING,
            `user_agent` STRING,
            `ip_address` STRING
        ) WITH (
            'connector' = 'kafka',
            'topic' = 'internal.platform.unified.events',
            'properties.bootstrap.servers' = '{brokers}',
            'value.format' = 'avro-confluent',
            'value.avro-confluent.schema-registry.url' = '{sr_url}'{sasl_props}
        )
    """)
    print("Registered Kafka sink: platform_unified_events")

    for table_name, cfg in sources.items():
        t_env.execute_sql(f"""
            CREATE TABLE {table_name} (
                {cfg['fields']},
                `proctime` AS PROCTIME()
            ) WITH (
                'connector' = 'kafka',
                'topic' = '{cfg['topic']}',
                'properties.bootstrap.servers' = '{brokers}',
                'properties.group.id' = '{cfg['group_id']}',
                'scan.startup.mode' = 'earliest-offset',
                'value.format' = 'avro-confluent',
                'value.avro-confluent.schema-registry.url' = '{sr_url}'{sasl_props}
            )
        """)
        print(f"Registered Kafka source: {table_name}")

def run_sql_statements(t_env, statement_set, sql_file_path):
    print(f"Reading SQL file: {sql_file_path}")
    with open(sql_file_path, 'r') as f:
        sql_content = resolve_env(f.read())

    statements = [s.strip() for s in sql_content.split(';') if s.strip()]

    for statement in statements:
        print(f"Executing statement: {statement[:50]}...")

        # Clean out comments to properly identify the statement type
        clean_stmt = re.sub(r'--.*', '', statement)
        clean_stmt = re.sub(r'/\*.*?\*/', '', clean_stmt, flags=re.DOTALL)

        if clean_stmt.strip().upper().startswith("INSERT INTO"):
            statement_set.add_insert_sql(statement)
        else:
            t_env.execute_sql(statement)

if __name__ == '__main__':
    if len(sys.argv) < 2:
        print("Usage: python sql_runner.py <path_to_sql_file> [additional_sql_files...]")
        sys.exit(1)

    env_obj = StreamExecutionEnvironment.get_execution_environment()
    env_obj.add_jars(
        "file:///opt/flink/usrlib/postgresql-42.7.2.jar",
        "file:///opt/flink/usrlib/flink-connector-jdbc-3.1.2-1.18.jar",
        "file:///opt/flink/usrlib/flink-sql-connector-kafka-3.0.1-1.18.jar",
        "file:///opt/flink/usrlib/flink-sql-avro-confluent-registry-1.18.0.jar",
        "file:///opt/flink/usrlib/kafka-schema-registry-client-7.6.0.jar",
        "file:///opt/flink/usrlib/flink-avro-1.18.0.jar",
        "file:///opt/flink/usrlib/iceberg-flink-runtime-1.18-1.5.0.jar",
        "file:///opt/flink/usrlib/flink-s3-fs-hadoop-1.18.0.jar"
    )
    settings = EnvironmentSettings.new_instance().in_streaming_mode().build()
    t_env = StreamTableEnvironment.create(env_obj, environment_settings=settings)

    # Register catalogs and Kafka source tables before running SQL files
    register_iceberg_catalog(t_env)
    register_kafka_sources(t_env)

    statement_set = t_env.create_statement_set()

    for sql_file in sys.argv[1:]:
        run_sql_statements(t_env, statement_set, sql_file)

    print("Executing all aggregated INSERT topologies...")
    statement_set.execute()
