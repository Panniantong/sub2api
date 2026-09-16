-- 242_global_model_pricing.sql
-- 全站模型定价覆盖：对指定模型（精确名或前缀通配，如 gpt-5*）强制使用
-- 此处设置的价格，全部分组/账号生效；未设置的模型走原定价链路。
CREATE TABLE IF NOT EXISTS global_model_pricing (
    id BIGSERIAL PRIMARY KEY,
    model_pattern VARCHAR(200) NOT NULL,
    billing_mode VARCHAR(16) NOT NULL DEFAULT 'token',
    input_price DOUBLE PRECISION,
    output_price DOUBLE PRECISION,
    cache_write_price DOUBLE PRECISION,
    cache_write_1h_price DOUBLE PRECISION,
    cache_read_price DOUBLE PRECISION,
    per_request_price DOUBLE PRECISION,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_global_model_pricing_pattern ON global_model_pricing(model_pattern);
CREATE INDEX IF NOT EXISTS idx_global_model_pricing_enabled ON global_model_pricing(enabled);
