"""
Credit Check Processor — PyFlink DataStream Job

Consumes UploadIntent events from internal.media.upload.intent.
For each event, atomically checks and deducts 1 credit via JDBC.
Produces either FileUploadApproved or FileUploadRejected to Kafka.

This is a standalone DataStream job (not part of sql_runner.py) because
Flink SQL cannot express conditional INSERT based on aggregate queries.
"""

import os
from datetime import datetime, timezone

import psycopg2
from pyflink.datastream import StreamExecutionEnvironment
from pyflink.table import EnvironmentSettings, StreamTableEnvironment

# Local dev defaults — overridden by environment variables in production
REDPANDA_BROKERS = os.environ.get("REDPANDA_BROKERS", "host.docker.internal:9092")
SCHEMA_REGISTRY_URL = os.environ.get("SCHEMA_REGISTRY_URL", "http://host.docker.internal:8081")
KAFKA_SECURITY_PROTOCOL = os.environ.get("KAFKA_SECURITY_PROTOCOL", "SASL_PLAINTEXT")
KAFKA_SASL_MECHANISM = os.environ.get("KAFKA_SASL_MECHANISM", "SCRAM-SHA-256")
KAFKA_SASL_USERNAME = os.environ.get("KAFKA_SASL_USERNAME", "flink-processor")
KAFKA_SASL_PASSWORD = os.environ.get("KAFKA_SASL_PASSWORD", "flink-processor-local-pw")

# Postgres connection for credit check
SUPABASE_DB_HOST = os.environ.get("SUPABASE_DB_HOST", "host.docker.internal")
SUPABASE_DB_PORT = os.environ.get("SUPABASE_DB_PORT", "54322")
SUPABASE_DB_NAME = os.environ.get("SUPABASE_DB_NAME", "postgres")
SUPABASE_DB_USER = os.environ.get("SUPABASE_DB_USER", "postgres")
SUPABASE_DB_PASSWORD = os.environ.get("SUPABASE_DB_PASSWORD", "postgres")

# Atomic check-and-deduct SQL: INSERT -1 only if balance >= 1
DEDUCT_SQL = """
INSERT INTO credit_ledger (user_id, amount, event_type, description, event_time)
SELECT %s, -1, 'credit.upload_deducted', %s, %s
WHERE (SELECT COALESCE(SUM(amount), 0) FROM credit_ledger WHERE user_id = %s) >= 1
RETURNING id
"""


def get_db_connection():
    """Create a Postgres connection for credit operations."""
    return psycopg2.connect(
        host=SUPABASE_DB_HOST,
        port=SUPABASE_DB_PORT,
        dbname=SUPABASE_DB_NAME,
        user=SUPABASE_DB_USER,
        password=SUPABASE_DB_PASSWORD,
        keepalives=1,
        keepalives_idle=30,
        keepalives_interval=10,
        keepalives_count=5,
    )


def run_credit_check_job():
    env = StreamExecutionEnvironment.get_execution_environment()
    env.add_jars(
        "file:///opt/flink/usrlib/flink-sql-connector-kafka-3.0.1-1.18.jar",
        "file:///opt/flink/usrlib/flink-sql-avro-confluent-registry-1.18.0.jar",
        "file:///opt/flink/usrlib/kafka-schema-registry-client-7.6.0.jar",
        "file:///opt/flink/usrlib/flink-avro-1.18.0.jar",
        "file:///opt/flink/usrlib/postgresql-42.7.2.jar",
    )
    settings = EnvironmentSettings.new_instance().in_streaming_mode().build()
    t_env = StreamTableEnvironment.create(env, environment_settings=settings)

    kafka_props = f"""
        'connector' = 'kafka',
        'properties.bootstrap.servers' = '{REDPANDA_BROKERS}',
        'properties.security.protocol' = '{KAFKA_SECURITY_PROTOCOL}',
        'properties.sasl.mechanism' = '{KAFKA_SASL_MECHANISM}',
        'properties.sasl.jaas.config' = 'org.apache.flink.kafka.shaded.org.apache.kafka.common.security.scram.ScramLoginModule required username="{KAFKA_SASL_USERNAME}" password="{KAFKA_SASL_PASSWORD}";',
        'value.format' = 'avro-confluent',
        'value.avro-confluent.schema-registry.url' = '{SCHEMA_REGISTRY_URL}'
    """

    # Source: UploadIntent from internal.media.upload.intent
    t_env.execute_sql(f"""
        CREATE TABLE upload_intent_source (
            `user_id` STRING,
            `file_name` STRING,
            `media_type` STRING,
            `file_size` BIGINT,
            `request_id` STRING,
            `request_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'internal.media.upload.intent',
            'properties.group.id' = 'flink-credit-check-consumer',
            'scan.startup.mode' = 'earliest-offset'
        )
    """)

    # Sink: FileUploadApproved to public.media.upload.approved
    t_env.execute_sql(f"""
        CREATE TABLE upload_approved_sink (
            `user_id` STRING,
            `file_name` STRING,
            `media_type` STRING,
            `file_size` BIGINT,
            `request_id` STRING,
            `request_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'public.media.upload.approved'
        )
    """)

    # Sink: FileUploadRejected to public.media.upload.rejected
    t_env.execute_sql(f"""
        CREATE TABLE upload_rejected_sink (
            `user_id` STRING,
            `file_name` STRING,
            `media_type` STRING,
            `file_size` BIGINT,
            `request_id` STRING,
            `reason` STRING,
            `rejected_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'public.media.upload.rejected'
        )
    """)

    # Convert source table to DataStream for custom processing
    intent_table = t_env.from_path("upload_intent_source")
    intent_stream = t_env.to_data_stream(intent_table)

    # Process each intent: check credit, produce approved or rejected
    from pyflink.common import Row
    from pyflink.common.typeinfo import Types
    from pyflink.datastream.functions import FlatMapFunction

    class CreditCheckFunction(FlatMapFunction):
        """Checks credit balance and deducts atomically. Emits (outcome, row) tuples."""

        def __init__(self):
            self.conn = None

        def open(self, runtime_context):
            self.conn = get_db_connection()

        def close(self):
            if self.conn:
                self.conn.close()

        def flat_map(self, value):
            user_id = value[0]  # user_id
            file_name = value[1]  # file_name
            media_type = value[2]  # media_type
            file_size = value[3]  # file_size
            request_id = value[4]  # request_id
            request_time = value[5]  # request_time

            description = f"Upload: {file_name}"
            event_time = datetime.now(timezone.utc).isoformat()

            try:
                cur = self.conn.cursor()
                cur.execute(DEDUCT_SQL, (user_id, description, event_time, user_id))
                result = cur.fetchone()
                self.conn.commit()

                if result:
                    # Credit deducted — approved
                    yield Row("approved", user_id, file_name, media_type, file_size, request_id, request_time, "", "")
                else:
                    # Insufficient credits — rejected
                    rejected_time = datetime.now(timezone.utc).isoformat()
                    yield Row(
                        "rejected",
                        user_id,
                        file_name,
                        media_type,
                        file_size,
                        request_id,
                        "",
                        "insufficient_credits",
                        rejected_time,
                    )

            except Exception as e:
                print(f"Credit check error for user {user_id}: {e}")
                try:
                    self.conn.rollback()
                except Exception:  # noqa: S110
                    pass  # Rollback failure is non-critical; reconnect follows
                # Reconnect on error
                try:
                    self.conn = get_db_connection()
                except Exception as reconnect_err:
                    print(f"Reconnect failed: {reconnect_err}")
                rejected_time = datetime.now(timezone.utc).isoformat()
                yield Row(
                    "rejected",
                    user_id,
                    file_name,
                    media_type,
                    file_size,
                    request_id,
                    "",
                    "internal_error",
                    rejected_time,
                )

    # Define output type: (outcome, user_id, file_name, media_type, file_size, request_id, request_time, reason, rejected_time)
    output_type = Types.ROW_NAMED(
        [
            "outcome",
            "user_id",
            "file_name",
            "media_type",
            "file_size",
            "request_id",
            "request_time",
            "reason",
            "rejected_time",
        ],
        [
            Types.STRING(),
            Types.STRING(),
            Types.STRING(),
            Types.STRING(),
            Types.LONG(),
            Types.STRING(),
            Types.STRING(),
            Types.STRING(),
            Types.STRING(),
        ],
    )

    result_stream = intent_stream.flat_map(CreditCheckFunction(), output_type=output_type)

    # Convert back to Table for Kafka sink routing
    result_table = t_env.from_data_stream(result_stream)

    # Split into approved and rejected via filtered views
    t_env.create_temporary_view("credit_check_results", result_table)

    # Route approved and rejected events to their sinks
    stmt_set = t_env.create_statement_set()
    stmt_set.add_insert_sql("""
        INSERT INTO upload_approved_sink
        SELECT user_id, file_name, media_type, file_size, request_id, request_time
        FROM credit_check_results
        WHERE outcome = 'approved'
    """)
    stmt_set.add_insert_sql("""
        INSERT INTO upload_rejected_sink
        SELECT user_id, file_name, media_type, file_size, request_id, reason, rejected_time
        FROM credit_check_results
        WHERE outcome = 'rejected'
    """)
    stmt_set.execute()

    print("Credit Check Processor started.")


if __name__ == "__main__":
    run_credit_check_job()
