CREATE TABLE IF NOT EXISTS codex_harvest_learning_epoch (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    generation BIGINT NOT NULL DEFAULT 1
);
INSERT INTO codex_harvest_learning_epoch(id, generation) VALUES (1, 1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS codex_harvest_nodes (
    id BIGSERIAL PRIMARY KEY,
    pool_id VARCHAR(64) NOT NULL,
    node_id VARCHAR(64) NOT NULL,
    node_name VARCHAR(512) NOT NULL,
    provider VARCHAR(512) NOT NULL,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    identity VARCHAR(64) NOT NULL,
    model VARCHAR(200) NOT NULL,
    blocks INTEGER NOT NULL,
    successes BIGINT NOT NULL DEFAULT 0,
    misses BIGINT NOT NULL DEFAULT 0,
    network_errors BIGINT NOT NULL DEFAULT 0,
    account_errors BIGINT NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_success TIMESTAMPTZ,
    cooldown_until TIMESTAMPTZ,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    last_result VARCHAR(40) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (pool_id, node_id, account_id, identity, model, blocks)
);
CREATE INDEX IF NOT EXISTS idx_codex_harvest_nodes_scope
    ON codex_harvest_nodes (pool_id, account_id, identity, model, blocks);
CREATE INDEX IF NOT EXISTS idx_codex_harvest_nodes_updated
    ON codex_harvest_nodes (updated_at DESC, id DESC);