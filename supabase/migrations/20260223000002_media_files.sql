-- Persistent media file metadata written by Flink on upload completion.
-- file_path is the claim check reference to object storage.
-- status column supports future soft-delete without schema migration.

CREATE TABLE public.media_files (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id TEXT NOT NULL,
  file_name TEXT NOT NULL,
  file_path TEXT NOT NULL,
  file_size BIGINT NOT NULL,
  media_type TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'active',
  upload_time TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE public.media_files ENABLE ROW LEVEL SECURITY;

CREATE POLICY "Users see own active files"
  ON public.media_files
  FOR SELECT TO authenticated
  USING (auth.uid()::text = user_id AND status = 'active');

ALTER PUBLICATION supabase_realtime ADD TABLE public.media_files;
ALTER TABLE public.media_files REPLICA IDENTITY FULL;
