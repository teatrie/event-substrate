-- Unified notification table for all client-facing processed events.
-- Producers: Flink JDBC sinks (identity.login, identity.signup, identity.signout)
--            Go message-consumer (user.message)
-- event_type uses {domain}.{entity} convention matching Kafka topic naming.
-- payload is a JSON TEXT string — shape varies per event_type, extensible without migrations.

CREATE TABLE public.user_notifications (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload TEXT NOT NULL DEFAULT '{}',
    event_time TEXT NOT NULL,
    visibility TEXT NOT NULL DEFAULT 'broadcast',
    recipient_id TEXT DEFAULT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE public.user_notifications ENABLE ROW LEVEL SECURITY;

-- Users see their own events, broadcast messages, or direct messages addressed to them
CREATE POLICY "Users see own events or visible messages"
ON public.user_notifications
FOR SELECT
TO authenticated
USING (
  auth.uid()::text = user_id
  OR (event_type = 'user.message' AND visibility = 'broadcast')
  OR (event_type = 'user.message' AND recipient_id = auth.uid()::text)
);

-- Only backend services can insert (not the frontend)
CREATE POLICY "Only backend can insert notifications"
ON public.user_notifications
FOR INSERT
WITH CHECK (false);

ALTER PUBLICATION supabase_realtime ADD TABLE public.user_notifications;
ALTER TABLE public.user_notifications REPLICA IDENTITY FULL;
