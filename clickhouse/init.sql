-- 1. Create Target MergeTree Table
CREATE TABLE IF NOT EXISTS default.unified_events
(
    event_id String,
    event_time DateTime64(3, 'UTC'),
    event_type String,
    user_id String,
    email String,
    device_id Nullable(String),
    user_agent Nullable(String),
    ip_address Nullable(String)
)
ENGINE = ReplacingMergeTree
ORDER BY (user_id, event_type, event_time, event_id);

-- 2. Create Kafka Engine Table to consume from Redpanda
CREATE TABLE IF NOT EXISTS default.kafka_unified_events
(
    event_id String,
    event_time String,
    event_type String,
    user_id String,
    email String,
    device_id Nullable(String),
    user_agent Nullable(String),
    ip_address Nullable(String)
)
ENGINE = Kafka
SETTINGS kafka_broker_list = 'redpanda:29092',
         kafka_topic_list = 'internal.platform.unified.events',
         kafka_group_name = 'clickhouse-consumer',
         kafka_format = 'AvroConfluent',
         format_avro_schema_registry_url = 'http://schema-registry:8081';

-- 3. Create Materialized View to pipe newly consumed Kafka records to the Target Table
CREATE MATERIALIZED VIEW IF NOT EXISTS default.unified_events_mv TO default.unified_events AS
SELECT
    event_id,
    -- Convert string ISO timestamp to ClickHouse DateTime64
    parseDateTime64BestEffort(event_time, 3, 'UTC') AS event_time,
    event_type,
    user_id,
    email,
    device_id,
    user_agent,
    ip_address
FROM default.kafka_unified_events;
