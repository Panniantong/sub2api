-- 鹈鹕测智用户展示：定时鹈鹕测试成功生成的 HTML 按展示分组各复制一份，
-- 独立于管理员的测试历史保留与清理（每组最多保留 N 张，可选超过 N 天自动清理）。
-- 旧版本二进制不读写这张表，可继续运行。
CREATE TABLE IF NOT EXISTS pelican_showcase_items (
    id               BIGSERIAL PRIMARY KEY,
    group_id         BIGINT      NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    source_result_id BIGINT      NOT NULL,
    model_id         TEXT        NOT NULL DEFAULT '',
    reasoning_effort TEXT        NOT NULL DEFAULT '',
    response_text    TEXT        NOT NULL,
    latency_ms       BIGINT      NOT NULL DEFAULT 0,
    generated_at     TIMESTAMPTZ NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (group_id, source_result_id)
);

CREATE INDEX IF NOT EXISTS idx_pelican_showcase_items_group_generated
    ON pelican_showcase_items (group_id, generated_at DESC, id DESC);

COMMENT ON TABLE pelican_showcase_items IS
    '鹈鹕测智用户展示快照：源结果被管理员历史清理后仍保留，按 pelican_showcase_config 的张数与天数清理';
COMMENT ON COLUMN pelican_showcase_items.source_result_id IS
    '来源 scheduled_test_results.id，仅用于去重与追溯，不设外键（源结果按管理员历史规则清理）';
