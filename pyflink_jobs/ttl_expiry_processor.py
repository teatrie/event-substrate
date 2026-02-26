"""
TTL Expiry Processor — PyFlink DataStream Job (D8)

Consumes TWO streams keyed by file_path:
  - public.media.upload.signed  (stream 1) — presigned URL generated
  - public.media.upload.events  (stream 2) — upload completed

When a signed event arrives a processing-time timer is registered for
TTL seconds in the future. On timer fire:
  - Upload completed → clear state, emit nothing
  - Upload NOT completed → emit FileUploadExpired → public.media.expired.events

Schema note: FileUploadExpired requires file_size / media_type, but
upload.signed does not carry those fields (they live on upload.approved).
Until the schema is extended, those fields default to 0 / "".
"""

import logging
import os
import json
from datetime import datetime, timezone

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Configuration — local dev defaults, overridden by environment variables
# ---------------------------------------------------------------------------

REDPANDA_BROKERS = os.environ.get("REDPANDA_BROKERS", "host.docker.internal:9092")
SCHEMA_REGISTRY_URL = os.environ.get(
    "SCHEMA_REGISTRY_URL", "http://host.docker.internal:8081"
)
KAFKA_SECURITY_PROTOCOL = os.environ.get("KAFKA_SECURITY_PROTOCOL", "SASL_PLAINTEXT")
KAFKA_SASL_MECHANISM = os.environ.get("KAFKA_SASL_MECHANISM", "SCRAM-SHA-256")
KAFKA_SASL_USERNAME = os.environ.get("KAFKA_SASL_USERNAME", "flink-processor")
KAFKA_SASL_PASSWORD = os.environ.get("KAFKA_SASL_PASSWORD", "flink-processor-local-pw")


# ---------------------------------------------------------------------------
# Shared Kafka config builder (mirrors pattern from credit_check_processor.py)
# ---------------------------------------------------------------------------

def build_kafka_props() -> str:
    """Return the common Kafka connector DDL properties for Redpanda with SASL."""
    jaas = (
        "org.apache.flink.kafka.shaded."
        "org.apache.kafka.common.security.scram.ScramLoginModule required "
        f'username="{KAFKA_SASL_USERNAME}" password="{KAFKA_SASL_PASSWORD}";'
    )
    return (
        f"'connector' = 'kafka', "
        f"'properties.bootstrap.servers' = '{REDPANDA_BROKERS}', "
        f"'properties.security.protocol' = '{KAFKA_SECURITY_PROTOCOL}', "
        f"'properties.sasl.mechanism' = '{KAFKA_SASL_MECHANISM}', "
        f"'properties.sasl.jaas.config' = '{jaas}', "
        f"'value.format' = 'avro-confluent', "
        f"'value.avro-confluent.schema-registry.url' = '{SCHEMA_REGISTRY_URL}'"
    )


# ---------------------------------------------------------------------------
# Core function — testable without a live Flink cluster
# ---------------------------------------------------------------------------

class TTLExpiryFunction:
    """
    Keyed co-process function: upload.signed (stream 1) + upload.events (stream 2).

    State per file_path key:
      _signed_state    — JSON-serialised upload.signed event dict
      _completed_state — bool flag, True once upload.events seen for this key

    On timer:
      completed=True  → clear state, yield nothing
      completed=False → yield FileUploadExpired dict, clear state
    """

    UPLOAD_TTL_SECONDS: int = 900

    def open(self, runtime_context) -> None:
        """Initialise ValueState handles via the Flink runtime context."""
        try:
            from pyflink.datastream.state import ValueStateDescriptor
            from pyflink.common.typeinfo import Types

            signed_desc = ValueStateDescriptor("signed_event", Types.STRING())
            completed_desc = ValueStateDescriptor("upload_completed", Types.BOOLEAN())
        except ImportError:
            # Unit tests supply MockRuntimeContext — descriptors are just tokens.
            signed_desc = "signed_event"
            completed_desc = "upload_completed"

        self._signed_state = runtime_context.get_state(signed_desc)
        self._completed_state = runtime_context.get_state(completed_desc)
        logger.info("TTLExpiryFunction initialised (TTL=%ds)", self.UPLOAD_TTL_SECONDS)

    def process_element1(self, value: dict, ctx) -> None:
        """Handle upload.signed: persist event and arm the expiry timer."""
        file_path = value.get("file_path", "")
        request_id = value.get("request_id", "")

        self._signed_state.update(json.dumps(value))
        self._completed_state.update(False)

        timer_service = ctx.timer_service()
        fire_at = timer_service.current_processing_time() + self.UPLOAD_TTL_SECONDS * 1000
        timer_service.register_processing_time_timer(fire_at)

        logger.debug(
            "Signed event stored for file_path=%s request_id=%s; timer at %d",
            file_path, request_id, fire_at,
        )

    def process_element2(self, value: dict, ctx) -> None:
        """Handle upload.events: mark upload as completed for this file_path."""
        file_path = value.get("file_path", "")
        self._completed_state.update(True)
        logger.debug("Upload completed for file_path=%s — timer disarmed", file_path)

    def on_timer(self, timestamp: int, ctx):
        """
        Timer callback. Yields a FileUploadExpired dict if the upload did not
        complete before the TTL elapsed. Always clears state on exit.
        """
        completed = self._completed_state.value()

        if completed:
            logger.debug("Timer fired but upload already completed — no event emitted")
        else:
            raw = self._signed_state.value()
            signed = json.loads(raw) if raw else {}
            expired_time = datetime.now(timezone.utc).isoformat()

            logger.warning(
                "Upload TTL expired for file_path=%s request_id=%s",
                signed.get("file_path"), signed.get("request_id"),
            )

            yield {
                "user_id": signed.get("user_id", ""),
                "file_path": signed.get("file_path", ""),
                "file_name": signed.get("file_name", ""),
                # file_size / media_type absent from upload.signed schema — see module docstring
                "file_size": signed.get("file_size", 0),
                "media_type": signed.get("media_type", ""),
                "request_id": signed.get("request_id", ""),
                "request_time": signed.get("signed_time", ""),
                "expired_time": expired_time,
            }

        self._signed_state.clear()
        self._completed_state.clear()


# ---------------------------------------------------------------------------
# Flink pipeline wiring (requires live cluster / pyflink installation)
# ---------------------------------------------------------------------------

def run_ttl_expiry_job():
    from pyflink.datastream import StreamExecutionEnvironment, KeyedCoProcessFunction
    from pyflink.common.typeinfo import Types
    from pyflink.common import Row
    from pyflink.table import StreamTableEnvironment, EnvironmentSettings

    logging.basicConfig(level=logging.INFO)

    env = StreamExecutionEnvironment.get_execution_environment()
    env.add_jars(
        "file:///opt/flink/usrlib/flink-sql-connector-kafka-3.0.1-1.18.jar",
        "file:///opt/flink/usrlib/flink-sql-avro-confluent-registry-1.18.0.jar",
        "file:///opt/flink/usrlib/kafka-schema-registry-client-7.6.0.jar",
        "file:///opt/flink/usrlib/flink-avro-1.18.0.jar",
    )
    settings = EnvironmentSettings.new_instance().in_streaming_mode().build()
    t_env = StreamTableEnvironment.create(env, environment_settings=settings)

    kafka_props = build_kafka_props()

    t_env.execute_sql(f"""
        CREATE TABLE upload_signed_source (
            `user_id`     STRING,
            `file_path`   STRING,
            `file_name`   STRING,
            `upload_url`  STRING,
            `expires_in`  INT,
            `request_id`  STRING,
            `signed_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'public.media.upload.signed',
            'properties.group.id' = 'flink-ttl-expiry-signed',
            'scan.startup.mode' = 'earliest-offset'
        )
    """)

    t_env.execute_sql(f"""
        CREATE TABLE upload_events_source (
            `user_id`     STRING,
            `email`       STRING,
            `file_path`   STRING,
            `file_name`   STRING,
            `file_size`   BIGINT,
            `media_type`  STRING,
            `upload_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'public.media.upload.events',
            'properties.group.id' = 'flink-ttl-expiry-completed',
            'scan.startup.mode' = 'earliest-offset'
        )
    """)

    t_env.execute_sql(f"""
        CREATE TABLE expired_events_sink (
            `user_id`      STRING,
            `file_path`    STRING,
            `file_name`    STRING,
            `file_size`    BIGINT,
            `media_type`   STRING,
            `request_id`   STRING,
            `request_time` STRING,
            `expired_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'public.media.expired.events'
        )
    """)

    signed_stream = t_env.to_data_stream(t_env.from_path("upload_signed_source"))
    events_stream = t_env.to_data_stream(t_env.from_path("upload_events_source"))

    keyed_signed = signed_stream.key_by(lambda r: r[1])  # file_path at index 1
    keyed_events = events_stream.key_by(lambda r: r[2])  # file_path at index 2

    output_type = Types.ROW_NAMED(
        [
            "user_id", "file_path", "file_name", "file_size",
            "media_type", "request_id", "request_time", "expired_time",
        ],
        [
            Types.STRING(), Types.STRING(), Types.STRING(), Types.LONG(),
            Types.STRING(), Types.STRING(), Types.STRING(), Types.STRING(),
        ],
    )

    class _FlinkTTLFunction(KeyedCoProcessFunction):
        """Thin Flink adapter that delegates to the testable TTLExpiryFunction."""

        def __init__(self):
            self._inner = TTLExpiryFunction()

        def open(self, ctx):
            self._inner.open(ctx)

        def process_element1(self, value, ctx, out):
            row_dict = {
                "user_id": value[0],
                "file_path": value[1],
                "file_name": value[2],
                "upload_url": value[3],
                "expires_in": value[4],
                "request_id": value[5],
                "signed_time": value[6],
            }
            self._inner.process_element1(row_dict, ctx)

        def process_element2(self, value, ctx, out):
            row_dict = {"user_id": value[0], "file_path": value[2]}
            self._inner.process_element2(row_dict, ctx)

        def on_timer(self, timestamp, ctx, out):
            for expired in self._inner.on_timer(timestamp, ctx):
                out.collect(
                    Row(
                        expired["user_id"],
                        expired["file_path"],
                        expired["file_name"],
                        expired["file_size"],
                        expired["media_type"],
                        expired["request_id"],
                        expired["request_time"],
                        expired["expired_time"],
                    )
                )

    result_stream = keyed_signed.connect(keyed_events).process(
        _FlinkTTLFunction(), output_type=output_type
    )

    result_table = t_env.from_data_stream(result_stream)
    t_env.create_temporary_view("expired_results", result_table)

    t_env.execute_sql("""
        INSERT INTO expired_events_sink
        SELECT user_id, file_path, file_name, file_size,
               media_type, request_id, request_time, expired_time
        FROM expired_results
    """)

    logger.info("TTL Expiry Processor started.")


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    run_ttl_expiry_job()
