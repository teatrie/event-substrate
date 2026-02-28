import os

from pyflink.datastream import StreamExecutionEnvironment
from pyflink.table import EnvironmentSettings, StreamTableEnvironment

# Local dev defaults — overridden by environment variables in production
REDPANDA_BROKERS = os.environ.get("REDPANDA_BROKERS", "host.docker.internal:9092")
SCHEMA_REGISTRY_URL = os.environ.get("SCHEMA_REGISTRY_URL", "http://host.docker.internal:8081")
KAFKA_SECURITY_PROTOCOL = os.environ.get("KAFKA_SECURITY_PROTOCOL", "SASL_PLAINTEXT")
KAFKA_SASL_MECHANISM = os.environ.get("KAFKA_SASL_MECHANISM", "SCRAM-SHA-256")
KAFKA_SASL_USERNAME = os.environ.get("KAFKA_SASL_USERNAME", "flink-processor")
KAFKA_SASL_PASSWORD = os.environ.get("KAFKA_SASL_PASSWORD", "flink-processor-local-pw")


def run_pyflink_echo_job():
    env = StreamExecutionEnvironment.get_execution_environment()
    env.add_jars(
        "file:///opt/flink/usrlib/flink-sql-connector-kafka-3.0.1-1.18.jar",
        "file:///opt/flink/usrlib/flink-sql-avro-confluent-registry-1.18.0.jar",
        "file:///opt/flink/usrlib/kafka-schema-registry-client-7.6.0.jar",
        "file:///opt/flink/usrlib/flink-avro-1.18.0.jar",
    )
    settings = EnvironmentSettings.new_instance().in_streaming_mode().build()
    t_env = StreamTableEnvironment.create(env, environment_settings=settings)

    # Register Kafka source and sink using shared config
    kafka_props = f"""
        'connector' = 'kafka',
        'properties.bootstrap.servers' = '{REDPANDA_BROKERS}',
        'properties.security.protocol' = '{KAFKA_SECURITY_PROTOCOL}',
        'properties.sasl.mechanism' = '{KAFKA_SASL_MECHANISM}',
        'properties.sasl.jaas.config' = 'org.apache.flink.kafka.shaded.org.apache.kafka.common.security.scram.ScramLoginModule required username="{KAFKA_SASL_USERNAME}" password="{KAFKA_SASL_PASSWORD}";',
        'value.format' = 'avro-confluent',
        'value.avro-confluent.schema-registry.url' = '{SCHEMA_REGISTRY_URL}'
    """

    fields = """`user_id` STRING, `email` STRING, `login_time` STRING,
        `device_id` STRING, `user_agent` STRING, `ip_address` STRING"""

    t_env.execute_sql(f"""
        CREATE TABLE login_events_source ({fields}) WITH (
            {kafka_props},
            'topic' = 'public.identity.login.events',
            'properties.group.id' = 'pyflink-echo-consumer',
            'scan.startup.mode' = 'earliest-offset'
        )
    """)

    t_env.execute_sql(f"""
        CREATE TABLE login_events_sink ({fields}) WITH (
            {kafka_props},
            'topic' = 'internal.identity.login.echo'
        )
    """)

    print("Starting PyFlink Echo Job...")
    t_env.execute_sql("""
        INSERT INTO login_events_sink
        SELECT user_id, email, login_time, device_id, user_agent, ip_address
        FROM login_events_source
    """)


if __name__ == "__main__":
    run_pyflink_echo_job()
