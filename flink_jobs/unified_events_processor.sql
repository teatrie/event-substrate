-- Kafka source tables and the platform_unified_events sink are registered
-- centrally by sql_runner.py (SASL JAAS config contains semicolons that
-- conflict with the SQL file statement splitter).

-- Execution (Union All)
INSERT INTO platform_unified_events
SELECT
  user_id || '_' || login_time AS event_id,
  login_time AS event_time,
  'LOGIN' AS event_type,
  user_id,
  email,
  device_id,
  user_agent,
  ip_address
FROM login_events

UNION ALL

SELECT
  user_id || '_' || signup_time AS event_id,
  signup_time AS event_time,
  'SIGNUP' AS event_type,
  user_id,
  email,
  CAST(NULL AS STRING) AS device_id,
  CAST(NULL AS STRING) AS user_agent,
  CAST(NULL AS STRING) AS ip_address
FROM signup_events

UNION ALL

SELECT
  user_id || '_' || signout_time AS event_id,
  signout_time AS event_time,
  'SIGNOUT' AS event_type,
  user_id,
  email,
  CAST(NULL AS STRING) AS device_id,
  CAST(NULL AS STRING) AS user_agent,
  CAST(NULL AS STRING) AS ip_address
FROM signout_events;
