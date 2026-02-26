-- Media Notification Processor
-- Consumes all media outcome events and writes to user_notifications.
-- Kafka source tables registered centrally by sql_runner.py:
--   upload_signed_events, upload_rejected_events, media_upload_events,
--   media_expired_events, download_signed_events, download_rejected_events,
--   media_delete_events, delete_rejected_events, upload_dead_letter_events

-- 1. Notification Sink (shared with credit_balance_processor — same table, different processor)
CREATE TABLE media_notification_sink (
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

-- 2. Upload URL ready notification (contains presigned URL for frontend)
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.upload_ready',
  JSON_OBJECT(
    'upload_url' VALUE upload_url,
    'file_path' VALUE file_path,
    'expires_in' VALUE expires_in,
    'request_id' VALUE request_id
  ),
  signed_time
FROM upload_signed_events;

-- 3. Upload rejected notification (insufficient credits)
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.upload_rejected',
  JSON_OBJECT(
    'reason' VALUE reason,
    'request_id' VALUE request_id,
    'file_name' VALUE file_name
  ),
  rejected_time
FROM upload_rejected_events;

-- 4. Upload completed notification
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.upload_completed',
  JSON_OBJECT(
    'file_path' VALUE file_path,
    'file_name' VALUE file_name,
    'file_size' VALUE file_size,
    'media_type' VALUE media_type
  ),
  upload_time
FROM media_upload_events;

-- 5. Upload expired notification (credit refunded)
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.upload_expired',
  JSON_OBJECT(
    'file_path' VALUE file_path,
    'file_name' VALUE file_name,
    'amount_refunded' VALUE 1,
    'request_id' VALUE request_id
  ),
  expired_time
FROM media_expired_events;

-- 6. Download URL ready notification
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.download_ready',
  JSON_OBJECT(
    'download_url' VALUE download_url,
    'file_path' VALUE file_path,
    'expires_in' VALUE expires_in,
    'request_id' VALUE request_id
  ),
  signed_time
FROM download_signed_events;

-- 7. Download rejected notification
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.download_rejected',
  JSON_OBJECT(
    'file_path' VALUE file_path,
    'reason' VALUE reason,
    'request_id' VALUE request_id
  ),
  rejected_time
FROM download_rejected_events;

-- 8. File deleted notification
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.file_deleted',
  JSON_OBJECT(
    'file_path' VALUE file_path,
    'file_name' VALUE file_name
  ),
  delete_time
FROM media_delete_events;

-- 9. Delete rejected notification
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.delete_rejected',
  JSON_OBJECT(
    'file_path' VALUE file_path,
    'reason' VALUE reason,
    'request_id' VALUE request_id
  ),
  rejected_time
FROM delete_rejected_events;

-- 10. Upload failed notification (dead-lettered after max retries)
INSERT INTO media_notification_sink
SELECT
  user_id,
  'media.upload_failed',
  JSON_OBJECT(
    'file_name' VALUE file_name,
    'failure_reason' VALUE failure_reason,
    'retry_count' VALUE retry_count
  ),
  dead_letter_time
FROM upload_dead_letter_events;
