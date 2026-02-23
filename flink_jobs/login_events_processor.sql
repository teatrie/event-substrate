-- Kafka source table "login_events" is registered centrally by sql_runner.py

-- 1. Notification Sink (append-only, no PK = pure INSERT)
CREATE TABLE login_notification_sink (
  `user_id` STRING,
  `event_type` STRING,
  `payload` STRING,
  `event_time` STRING
) WITH (
  'connector' = 'jdbc',
  'url' = '${SUPABASE_DB_JDBC_URL}',
  'table-name' = 'public.user_notifications',
  'username' = '${SUPABASE_DB_USER}',
  'password' = '${SUPABASE_DB_PASSWORD}',
  'sink.buffer-flush.max-rows' = '1',
  'sink.buffer-flush.interval' = '1s'
);

-- 2. Execute the Continuous Streaming Query
INSERT INTO login_notification_sink
SELECT
  user_id,
  'identity.login',
  JSON_OBJECT('email' VALUE email, 'device_id' VALUE device_id, 'user_agent' VALUE user_agent, 'ip_address' VALUE ip_address),
  login_time
FROM login_events;
