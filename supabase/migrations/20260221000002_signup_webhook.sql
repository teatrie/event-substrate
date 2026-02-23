-- Enable pg_net extension if it is not already enabled
create extension if not exists pg_net;

-- Create the webhook function
create or replace function public.handle_user_signup()
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
  payload := jsonb_build_object(
    'user_id', new.id,
    'email', new.email,
    'signup_time', new.created_at
  );

  -- Send the asynchronous HTTP POST request to the API Gateway using pg_net
  perform net.http_post(
    url := gateway_url || '/webhooks/signup',
    body := payload,
    headers := jsonb_build_object('Content-Type', 'application/json', 'X-Webhook-Secret', webhook_secret)
  );

  return new;
end;
$$ language plpgsql security definer;

-- Create the trigger on the auth.users table
drop trigger if exists on_auth_user_signup on auth.users;
create trigger on_auth_user_signup
  after insert on auth.users
  for each row
  execute function public.handle_user_signup();
