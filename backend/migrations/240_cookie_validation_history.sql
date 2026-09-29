CREATE TABLE IF NOT EXISTS cookie_validation_history (
 id text PRIMARY KEY,
 account_id bigint NOT NULL,
 host text NOT NULL DEFAULT '',
 attempt_id text NOT NULL,
 created_at timestamptz NOT NULL,
 payload jsonb NOT NULL
);
CREATE INDEX IF NOT EXISTS cookie_validation_history_account_time ON cookie_validation_history(account_id, created_at DESC);
CREATE INDEX IF NOT EXISTS cookie_validation_history_attempt ON cookie_validation_history(attempt_id);
CREATE INDEX IF NOT EXISTS cookie_validation_history_time ON cookie_validation_history(created_at DESC);
INSERT INTO cookie_validation_history(id, account_id, host, attempt_id, created_at, payload)
SELECT item->>'id', (item->>'account_id')::bigint, COALESCE(item->>'host',''),
 COALESCE(NULLIF(item->>'attempt_id',''),item->>'id'), (item->>'created_at')::timestamptz, item
FROM settings, LATERAL jsonb_array_elements(value::jsonb) item
WHERE key='openai_cookie_validation_logs' AND item->>'id' IS NOT NULL
 AND COALESCE(item->>'binding_status','') NOT IN ('rotation_started','rotation_waiting','rotation_candidate')
ON CONFLICT DO NOTHING;
