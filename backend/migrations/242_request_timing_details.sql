-- Additive diagnostics storage; old binaries do not read or write this table.
-- No FK: usage_logs can be partitioned; retention also removes orphaned details.
CREATE TABLE IF NOT EXISTS request_timing_details (
    usage_log_id BIGINT NOT NULL,
    trace_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    detail JSONB NOT NULL,
    PRIMARY KEY (usage_log_id, trace_id)
);
CREATE INDEX IF NOT EXISTS request_timing_details_created_at_idx ON request_timing_details(created_at);
