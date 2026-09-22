CREATE TABLE IF NOT EXISTS codex_harvest_flow_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) NOT NULL UNIQUE,
    at TIMESTAMPTZ NOT NULL,
    stage VARCHAR(32) NOT NULL DEFAULT '',
    kind VARCHAR(32) NOT NULL DEFAULT '',
    account_id BIGINT NOT NULL DEFAULT 0,
    account_name VARCHAR(80) NOT NULL DEFAULT '',
    model VARCHAR(64) NOT NULL DEFAULT '',
    node VARCHAR(160) NOT NULL DEFAULT '',
    http_status INTEGER NOT NULL DEFAULT 0,
    length INTEGER NOT NULL DEFAULT 0,
    blocks INTEGER NOT NULL DEFAULT 0,
    expected_length INTEGER NOT NULL DEFAULT 0,
    expected_blocks INTEGER NOT NULL DEFAULT 0,
    accepted BOOLEAN NOT NULL DEFAULT FALSE,
    standby BOOLEAN NOT NULL DEFAULT FALSE,
    result VARCHAR(64) NOT NULL DEFAULT '',
    reason VARCHAR(64) NOT NULL DEFAULT '',
    detail VARCHAR(240) NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_codex_harvest_flow_events_at
    ON codex_harvest_flow_events (at ASC, id ASC);