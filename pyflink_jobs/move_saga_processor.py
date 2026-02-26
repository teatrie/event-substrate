"""
Move Saga Processor — PyFlink DataStream Job

Consumes TWO streams keyed by permanent file path:
  - internal.media.upload.received  (stream 1) — upload detected in staging
  - internal.media.file.ready       (stream 2) — file appeared in permanent prefix

When an upload.received arrives, a processing-time timer is registered for
MOVE_TIMEOUT_SECONDS (120s). On timer fire:
  - Both present → emit UploadConfirmed → public.media.upload.confirmed
  - Only received, retry_count < 3 → emit UploadRetry → internal.media.upload.retry
  - Only received, retry_count >= 3 → emit UploadDeadLetter → internal.media.upload.dead-letter
"""

import logging
import os
import json
from datetime import datetime, timezone

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------

REDPANDA_BROKERS = os.environ.get("REDPANDA_BROKERS", "host.docker.internal:9092")
SCHEMA_REGISTRY_URL = os.environ.get("SCHEMA_REGISTRY_URL", "http://host.docker.internal:8081")
KAFKA_SECURITY_PROTOCOL = os.environ.get("KAFKA_SECURITY_PROTOCOL", "SASL_PLAINTEXT")
KAFKA_SASL_MECHANISM = os.environ.get("KAFKA_SASL_MECHANISM", "SCRAM-SHA-256")
KAFKA_SASL_USERNAME = os.environ.get("KAFKA_SASL_USERNAME", "flink-processor")
KAFKA_SASL_PASSWORD = os.environ.get("KAFKA_SASL_PASSWORD", "flink-processor-local-pw")


def build_kafka_props() -> str:
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

class MoveSagaFunction:
    """
    Keyed co-process function: upload.received (stream 1) + file.ready (stream 2).

    State per permanent_path key:
      _received_state — JSON-serialised upload.received event dict
      _ready_state    — bool flag, True once file.ready seen for this key

    On timer:
      both present      → yield confirmed dict
      only received, retry < MAX → yield retry dict
      only received, retry >= MAX → yield dead_letter dict
    """

    MAX_RETRIES: int = 3
    MOVE_TIMEOUT_SECONDS: int = int(os.environ.get("MOVE_TIMEOUT_SECONDS", "120"))

    def open(self, runtime_context) -> None:
        try:
            from pyflink.datastream.state import ValueStateDescriptor
            from pyflink.common.typeinfo import Types

            received_desc = ValueStateDescriptor("received_event", Types.STRING())
            ready_desc = ValueStateDescriptor("file_ready", Types.BOOLEAN())
        except ImportError:
            received_desc = "received_event"
            ready_desc = "file_ready"

        self._received_state = runtime_context.get_state(received_desc)
        self._ready_state = runtime_context.get_state(ready_desc)
        logger.info("MoveSagaFunction initialised (timeout=%ds, max_retries=%d)",
                     self.MOVE_TIMEOUT_SECONDS, self.MAX_RETRIES)

    def _check_fast_confirm(self):
        """Fast path: if both events already present, emit confirmed immediately."""
        raw = self._received_state.value()
        ready = self._ready_state.value()
        if raw and ready:
            received = json.loads(raw)
            self._received_state.clear()
            self._ready_state.clear()
            logger.info("Fast-path confirmed for permanent_path=%s", received.get("permanent_path", ""))
            return {
                "_type": "confirmed",
                "user_id": received.get("user_id", ""),
                "email": received.get("email", ""),
                "file_path": received.get("permanent_path", ""),
                "file_name": received.get("file_name", ""),
                "file_size": received.get("file_size", 0),
                "media_type": received.get("media_type", ""),
                "upload_time": received.get("upload_time", ""),
            }
        return None

    def process_element1(self, value: dict, ctx):
        """Handle upload.received: persist event, arm timer, fast-path if ready."""
        self._received_state.update(json.dumps(value))

        timer_service = ctx.timer_service()
        fire_at = timer_service.current_processing_time() + self.MOVE_TIMEOUT_SECONDS * 1000
        timer_service.register_processing_time_timer(fire_at)

        logger.debug("Upload received stored for permanent_path=%s, timer at %d",
                      value.get("permanent_path", ""), fire_at)
        return self._check_fast_confirm()

    def process_element2(self, value: dict, ctx):
        """Handle file.ready: mark ready, fast-path if received already stored."""
        self._ready_state.update(True)
        logger.debug("File ready for path=%s", value.get("file_path", ""))
        return self._check_fast_confirm()

    def on_timer(self, timestamp: int, ctx):
        """
        Timer callback. Three outcomes based on state:
        1. Both received + ready → confirmed
        2. Only received, retry_count < MAX_RETRIES → retry
        3. Only received, retry_count >= MAX_RETRIES → dead_letter
        """
        ready = self._ready_state.value()
        raw = self._received_state.value()
        received = json.loads(raw) if raw else {}

        try:
            if ready and received:
                yield {
                    "_type": "confirmed",
                    "user_id": received.get("user_id", ""),
                    "email": received.get("email", ""),
                    "file_path": received.get("permanent_path", ""),
                    "file_name": received.get("file_name", ""),
                    "file_size": received.get("file_size", 0),
                    "media_type": received.get("media_type", ""),
                    "upload_time": received.get("upload_time", ""),
                }
            elif received:
                retry_count = received.get("retry_count", 0)

                if retry_count < self.MAX_RETRIES:
                    now = datetime.now(timezone.utc).isoformat()
                    yield {
                        "_type": "retry",
                        "user_id": received.get("user_id", ""),
                        "email": received.get("email", ""),
                        "file_path": received.get("file_path", ""),
                        "permanent_path": received.get("permanent_path", ""),
                        "file_name": received.get("file_name", ""),
                        "file_size": received.get("file_size", 0),
                        "media_type": received.get("media_type", ""),
                        "upload_time": received.get("upload_time", ""),
                        "retry_count": retry_count,
                        "retry_time": now,
                    }
                    logger.warning("Move timeout for %s, retry_count=%d — emitting retry",
                                   received.get("permanent_path"), retry_count)
                else:
                    now = datetime.now(timezone.utc).isoformat()
                    yield {
                        "_type": "dead_letter",
                        "user_id": received.get("user_id", ""),
                        "email": received.get("email", ""),
                        "file_path": received.get("file_path", ""),
                        "permanent_path": received.get("permanent_path", ""),
                        "file_name": received.get("file_name", ""),
                        "file_size": received.get("file_size", 0),
                        "media_type": received.get("media_type", ""),
                        "upload_time": received.get("upload_time", ""),
                        "retry_count": retry_count,
                        "failure_reason": "max retries exceeded",
                        "dead_letter_time": now,
                    }
                    logger.error("Move failed for %s after %d retries — dead-lettering",
                                 received.get("permanent_path"), retry_count)
        finally:
            self._received_state.clear()
            self._ready_state.clear()


# ---------------------------------------------------------------------------
# Flink pipeline wiring
# ---------------------------------------------------------------------------

def run_move_saga_job():
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

    # Source: upload.received
    t_env.execute_sql(f"""
        CREATE TABLE upload_received_source (
            `user_id`        STRING,
            `email`          STRING,
            `file_path`      STRING,
            `file_name`      STRING,
            `file_size`      BIGINT,
            `media_type`     STRING,
            `upload_time`    STRING,
            `permanent_path` STRING,
            `retry_count`    INT
        ) WITH (
            {kafka_props},
            'topic' = 'internal.media.upload.received',
            'properties.group.id' = 'pyflink-move-saga-received',
            'scan.startup.mode' = 'earliest-offset'
        )
    """)

    # Source: file.ready
    t_env.execute_sql(f"""
        CREATE TABLE file_ready_source (
            `file_path`   STRING,
            `file_size`   BIGINT,
            `upload_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'internal.media.file.ready',
            'properties.group.id' = 'pyflink-move-saga-ready',
            'scan.startup.mode' = 'earliest-offset'
        )
    """)

    # Sinks
    t_env.execute_sql(f"""
        CREATE TABLE upload_confirmed_sink (
            `user_id`     STRING,
            `email`       STRING,
            `file_path`   STRING,
            `file_name`   STRING,
            `file_size`   BIGINT,
            `media_type`  STRING,
            `upload_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'public.media.upload.confirmed'
        )
    """)

    t_env.execute_sql(f"""
        CREATE TABLE upload_retry_sink (
            `user_id`        STRING,
            `email`          STRING,
            `file_path`      STRING,
            `permanent_path` STRING,
            `file_name`      STRING,
            `file_size`      BIGINT,
            `media_type`     STRING,
            `upload_time`    STRING,
            `retry_count`    INT,
            `retry_time`     STRING
        ) WITH (
            {kafka_props},
            'topic' = 'internal.media.upload.retry'
        )
    """)

    t_env.execute_sql(f"""
        CREATE TABLE upload_dead_letter_sink (
            `user_id`          STRING,
            `email`            STRING,
            `file_path`        STRING,
            `permanent_path`   STRING,
            `file_name`        STRING,
            `file_size`        BIGINT,
            `media_type`       STRING,
            `upload_time`      STRING,
            `retry_count`      INT,
            `failure_reason`   STRING,
            `dead_letter_time` STRING
        ) WITH (
            {kafka_props},
            'topic' = 'internal.media.upload.dead-letter'
        )
    """)

    received_stream = t_env.to_data_stream(t_env.from_path("upload_received_source"))
    ready_stream = t_env.to_data_stream(t_env.from_path("file_ready_source"))

    keyed_received = received_stream.key_by(lambda r: r[7])  # permanent_path
    keyed_ready = ready_stream.key_by(lambda r: r[0])         # file_path

    # Single wide output type with _type discriminator — avoids side outputs
    # (PyFlink's on_timer context doesn't support ctx.output() for side outputs)
    saga_output_type = Types.ROW_NAMED(
        ["_type", "user_id", "email", "file_path", "permanent_path", "file_name",
         "file_size", "media_type", "upload_time", "retry_count", "retry_time",
         "failure_reason", "dead_letter_time"],
        [Types.STRING(), Types.STRING(), Types.STRING(), Types.STRING(), Types.STRING(),
         Types.STRING(), Types.LONG(), Types.STRING(), Types.STRING(), Types.INT(),
         Types.STRING(), Types.STRING(), Types.STRING()],
    )

    def _to_output_row(result):
        """Convert a MoveSagaFunction result dict to a wide output Row."""
        return Row(
            result.get("_type", ""),
            result.get("user_id", ""),
            result.get("email", ""),
            result.get("file_path", ""),
            result.get("permanent_path", ""),
            result.get("file_name", ""),
            result.get("file_size", 0),
            result.get("media_type", ""),
            result.get("upload_time", ""),
            result.get("retry_count", 0),
            result.get("retry_time", ""),
            result.get("failure_reason", ""),
            result.get("dead_letter_time", ""),
        )

    class _FlinkMoveSagaFunction(KeyedCoProcessFunction):
        def __init__(self):
            self._inner = MoveSagaFunction()

        def open(self, ctx):
            self._inner.open(ctx)

        def process_element1(self, value, ctx):
            row_dict = {
                "user_id": value[0], "email": value[1], "file_path": value[2],
                "file_name": value[3], "file_size": value[4], "media_type": value[5],
                "upload_time": value[6], "permanent_path": value[7], "retry_count": value[8],
            }
            result = self._inner.process_element1(row_dict, ctx)
            if result:
                yield _to_output_row(result)

        def process_element2(self, value, ctx):
            row_dict = {"file_path": value[0], "file_size": value[1], "upload_time": value[2]}
            result = self._inner.process_element2(row_dict, ctx)
            if result:
                yield _to_output_row(result)

        def on_timer(self, timestamp, ctx):
            for result in self._inner.on_timer(timestamp, ctx):
                yield _to_output_row(result)

    result_stream = keyed_received.connect(keyed_ready).process(
        _FlinkMoveSagaFunction(), output_type=saga_output_type
    )

    # Single stream → table, route by _type discriminator
    saga_table = t_env.from_data_stream(result_stream)
    t_env.create_temporary_view("saga_results", saga_table)

    # Use StatementSet to batch all INSERTs into a single execute
    # (Flink 1.18 allows only one execute per environment)
    stmt_set = t_env.create_statement_set()
    stmt_set.add_insert_sql("""
        INSERT INTO upload_confirmed_sink
        SELECT user_id, email, file_path, file_name, file_size, media_type, upload_time
        FROM saga_results WHERE _type = 'confirmed'
    """)
    stmt_set.add_insert_sql("""
        INSERT INTO upload_retry_sink
        SELECT user_id, email, file_path, permanent_path, file_name, file_size,
               media_type, upload_time, retry_count, retry_time
        FROM saga_results WHERE _type = 'retry'
    """)
    stmt_set.add_insert_sql("""
        INSERT INTO upload_dead_letter_sink
        SELECT user_id, email, file_path, permanent_path, file_name, file_size,
               media_type, upload_time, retry_count, failure_reason, dead_letter_time
        FROM saga_results WHERE _type = 'dead_letter'
    """)
    stmt_set.execute()

    logger.info("Move Saga Processor started.")


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    run_move_saga_job()
