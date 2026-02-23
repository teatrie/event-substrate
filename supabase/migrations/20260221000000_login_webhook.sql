-- Enable pg_net extension if it is not already enabled
create extension if not exists pg_net;

-- Create the webhook function
create or replace function public.handle_user_login()
returns trigger as $$
declare
  payload jsonb;
  webhook_secret text;
  gateway_url text;
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

  -- Construct the JSON payload with user details
  -- (Assuming user_agent and ip are either passed into raw_user_meta_data by the frontend
  -- or we emit null and the API Gateway enriches it later)
  payload := jsonb_build_object(
    'user_id', new.id,
    'email', new.email,
    'login_time', new.last_sign_in_at,
    'device_id', new.raw_user_meta_data->>'device_id',
    'user_agent', new.raw_user_meta_data->>'user_agent',
    'ip_address', new.raw_user_meta_data->>'ip_address'
  );

  -- Send the asynchronous HTTP POST request to the API Gateway using pg_net
  -- Note: This is a fire-and-forget approach for local dev. If the gateway is down, the PG_NET call will fail and data won't stream.
  -- In a production environment, use a robust CDC pattern (like Debezium) or an Outbox table to guarantee delivery.
  perform net.http_post(
    url := gateway_url || '/webhooks/login',
    body := payload,
    headers := jsonb_build_object('Content-Type', 'application/json', 'X-Webhook-Secret', webhook_secret)
  );

  return new;
end;
$$ language plpgsql security definer;

-- Create the trigger on the auth.users table
drop trigger if exists on_auth_user_login on auth.users;
create trigger on_auth_user_login
  after update on auth.users
  for each row
  when (old.last_sign_in_at is distinct from new.last_sign_in_at)
  execute function public.handle_user_login();
