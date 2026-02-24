-- Append-only credit ledger for event-sourced credit balance tracking.
-- Flink writes +2 on signup, -1 on media upload. Balance = SUM(amount).

CREATE TABLE public.credit_ledger (
  id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  user_id TEXT NOT NULL,
  amount INTEGER NOT NULL,
  event_type TEXT NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  event_time TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Aggregated balance view for API Gateway credit checks
CREATE VIEW public.user_credit_balances AS
  SELECT user_id, COALESCE(SUM(amount), 0) AS balance
  FROM public.credit_ledger
  GROUP BY user_id;

ALTER TABLE public.credit_ledger ENABLE ROW LEVEL SECURITY;

CREATE POLICY "Users see own credits"
  ON public.credit_ledger
  FOR SELECT TO authenticated
  USING (auth.uid()::text = user_id);

ALTER PUBLICATION supabase_realtime ADD TABLE public.credit_ledger;
ALTER TABLE public.credit_ledger REPLICA IDENTITY FULL;
