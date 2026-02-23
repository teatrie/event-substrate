-- Enable pg_net extension if it is not already enabled
create extension if not exists pg_net;

-- Create the webhook function
create or replace function public.handle_user_signout()
returns trigger as $$
declare
  payload jsonb;
  webhook_secret text;
  gateway_url text;
  user_email text;
begin
  -- Fetch secrets from Supabase Vault (fallback to local dev defaults if not seeded yet)
  select decrypted_secret into webhook_secret from vault.decrypted_secrets where name = 'webhook_secret';
  if webhook_secret is null then
    webhook_secret := 'super-secret-webhook-key';
  end if;

  select decrypted_secret into gateway_url from vault.decrypted_secrets where name = 'gateway_url';
  if gateway_url is null then
    gateway_url := 'http://host.docker.internal:8080';
  end if;

  -- Fetch the user's email since auth.sessions doesn't store it
  select email into user_email from auth.users where id = old.user_id;

  -- Construct the JSON payload with user details
  payload := jsonb_build_object(
    'user_id', old.user_id,
    'email', user_email,
    'signout_time', now(),
    'device_id', null,
    'user_agent', old.user_agent,
    'ip_address', host(old.ip)
  );

  -- Send the asynchronous HTTP POST request to the API Gateway using pg_net
  perform net.http_post(
    url := gateway_url || '/webhooks/signout',
    body := payload,
    headers := jsonb_build_object('Content-Type', 'application/json', 'X-Webhook-Secret', webhook_secret)
  );

  return old;
end;
$$ language plpgsql security definer;

-- Create the trigger on the auth.sessions table
drop trigger if exists on_auth_session_delete on auth.sessions;
create trigger on_auth_session_delete
  after delete on auth.sessions
  for each row
  execute function public.handle_user_signout();
