-- Request/response state and account-bound request diagnostics for admin usage records.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS request_debug JSONB;
