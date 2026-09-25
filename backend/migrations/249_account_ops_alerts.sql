-- One durable, coalesced alert per account and error category. Raw upstream responses are never stored.
CREATE TABLE IF NOT EXISTS account_ops_alerts (
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 kind TEXT NOT NULL CHECK(kind IN ('balance_low','weekly_quota')),
 account_name TEXT NOT NULL,
 signal TEXT NOT NULL,
 http_status INTEGER NOT NULL,
 first_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_seen TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 occurrences BIGINT NOT NULL DEFAULT 1,
 state TEXT NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','sending','sent','failed','suppressed')),
 last_sent_at TIMESTAMPTZ,
 next_send_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 attempts INTEGER NOT NULL DEFAULT 0,
 lease TEXT NOT NULL DEFAULT '',
 lease_until TIMESTAMPTZ,
 PRIMARY KEY(account_id,kind)
);
CREATE INDEX IF NOT EXISTS account_ops_alerts_delivery ON account_ops_alerts(state,next_send_at);
