-- Additive only: durable account capability tests; existing account/user rows stay intact.
CREATE TABLE IF NOT EXISTS test_settings (
    test_type VARCHAR(64) PRIMARY KEY,
    enabled BOOLEAN NOT NULL DEFAULT true,
    user_visible BOOLEAN NOT NULL DEFAULT false,
    config JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO test_settings(test_type, config) VALUES
('pelican', '{"prompt":"请只输出一个独立、有效的 SVG，绘制一只骑自行车的鹈鹕。画面包含两个车轮、车架、鹈鹕身体、长喙和脚踏关系。使用 viewBox 和静态图形属性（fill、stroke、transform 等），不要使用 style、class、脚本、外部资源、foreignObject 或图片嵌入。","model":"","evaluator":"svg_structure","expected_answer":"","timeout_seconds":300}'),
('candy', '{"prompt":"盒子里有 24 颗糖。小明取走总数的四分之一，小红随后取走剩余糖的一半，最后小明放回 3 颗。盒子里现在有多少颗糖？请给出简短推导，并在最后单独一行输出 ANSWER: 数字。","model":"","evaluator":"exact_answer","expected_answer":"12","timeout_seconds":180}')
ON CONFLICT(test_type) DO NOTHING;

CREATE TABLE IF NOT EXISTS account_tests (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    test_type VARCHAR(64) NOT NULL REFERENCES test_settings(test_type) ON DELETE RESTRICT,
    status VARCHAR(32) NOT NULL DEFAULT 'queued'
        CHECK(status IN ('queued','running','success','failed','rate_limited','account_error','model_error','suspected_degradation')),
    score DOUBLE PRECISION CHECK(score IS NULL OR (score >= 0 AND score <= 100)),
    result TEXT NOT NULL DEFAULT '',
    result_image TEXT NOT NULL DEFAULT '',
    input TEXT NOT NULL,
    raw_response TEXT NOT NULL DEFAULT '',
    raw_truncated BOOLEAN NOT NULL DEFAULT false,
    error_message TEXT NOT NULL DEFAULT '',
    duration_ms BIGINT NOT NULL DEFAULT 0,
    model VARCHAR(200) NOT NULL DEFAULT '',
    anti_degradation BOOLEAN NOT NULL,
    config_snapshot JSONB NOT NULL,
    evaluation JSONB NOT NULL DEFAULT '{}',
    requested_by BIGINT NOT NULL,
    lease_token VARCHAR(64),
    lease_until TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_account_tests_account_history ON account_tests(account_id, test_type, id DESC);
CREATE INDEX IF NOT EXISTS idx_account_tests_created ON account_tests(created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_account_tests_queue ON account_tests(status, id) WHERE status IN ('queued','running');
CREATE UNIQUE INDEX IF NOT EXISTS idx_account_tests_active ON account_tests(account_id, test_type) WHERE status IN ('queued','running');

CREATE TABLE IF NOT EXISTS intelligent_test_requests (
    actor_id BIGINT NOT NULL,
    request_key VARCHAR(100) NOT NULL,
    fingerprint VARCHAR(64) NOT NULL,
    record_ids BIGINT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY(actor_id, request_key)
);
