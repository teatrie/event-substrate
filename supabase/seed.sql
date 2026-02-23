-- Seed the encrypted Postgres Vault with the API Gateway Webhook Secret
-- This allows the login/signup triggers to dynamically retrieve the key 
-- without hardcoding plaintext credentials into git history.

SELECT vault.create_secret(
  'super-secret-webhook-key',  
  'webhook_secret',
  'Primary authorization key for Go API Gateway pg_net payloads'
);
