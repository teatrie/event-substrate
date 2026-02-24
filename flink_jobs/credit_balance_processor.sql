-- Kafka source tables "signup_events" and "media_upload_events" are registered centrally by sql_runner.py

-- 1. Credit Ledger Sink (append-only, no PK = pure INSERT)
CREATE TABLE credit_ledger_sink (
  `user_id` STRING,
  `amount` INT,
  `event_type` STRING,
  `description` STRING,
  `event_time` STRING
) WITH (
  'connector' = 'jdbc',
  'url' = '${SUPABASE_DB_JDBC_URL}',
  'table-name' = 'public.credit_ledger',
  'username' = '${SUPABASE_DB_USER}',
  'password' = '${SUPABASE_DB_PASSWORD}',
  'sink.buffer-flush.max-rows' = '1',
  'sink.buffer-flush.interval' = '1s'
);

-- 2. Media Files Sink (append-only, no PK = pure INSERT)
CREATE TABLE media_files_sink (
  `user_id` STRING,
  `file_name` STRING,
  `file_path` STRING,
  `file_size` BIGINT,
  `media_type` STRING,
  `status` STRING,
  `upload_time` STRING
) WITH (
  'connector' = 'jdbc',
  'url' = '${SUPABASE_DB_JDBC_URL}',
  'table-name' = 'public.media_files',
  'username' = '${SUPABASE_DB_USER}',
  'password' = '${SUPABASE_DB_PASSWORD}',
  'sink.buffer-flush.max-rows' = '1',
  'sink.buffer-flush.interval' = '1s'
);

-- 3. Credit Notification Sink (append-only, no PK = pure INSERT)
CREATE TABLE credit_notification_sink (
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

-- 4. INSERT: Signup bonus -> credit_ledger
INSERT INTO credit_ledger_sink
SELECT
  user_id,
  2,
  'credit.signup_bonus',
  'Welcome bonus: 2 credits',
  signup_time
FROM signup_events;

-- 5. INSERT: Media upload deduction -> credit_ledger
INSERT INTO credit_ledger_sink
SELECT
  user_id,
  -1,
  'credit.media_upload',
  CONCAT('Upload: ', file_name),
  upload_time
FROM media_upload_events;

-- 6. INSERT: Media upload -> media_files
INSERT INTO media_files_sink
SELECT
  user_id,
  file_name,
  file_path,
  file_size,
  media_type,
  'active',
  upload_time
FROM media_upload_events;

-- 7. INSERT: Signup notification -> credit_notification
INSERT INTO credit_notification_sink
SELECT
  user_id,
  'credit.balance_changed',
  JSON_OBJECT('amount' VALUE 2, 'reason' VALUE 'signup_bonus'),
  signup_time
FROM signup_events;

-- 8. INSERT: Media upload notification -> credit_notification
INSERT INTO credit_notification_sink
SELECT
  user_id,
  'credit.balance_changed',
  JSON_OBJECT('amount' VALUE -1, 'reason' VALUE 'media_upload', 'file_name' VALUE file_name),
  upload_time
FROM media_upload_events;
