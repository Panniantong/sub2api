-- 用户在单个分组内被禁用的模型清单，与专属倍率 / RPM 共用一行。
-- NULL 表示不限制；rate_multiplier、rpm_override、denied_models 都为 NULL 时整行删除。
ALTER TABLE user_group_rate_multipliers
    ADD COLUMN IF NOT EXISTS denied_models JSONB NULL;

COMMENT ON COLUMN user_group_rate_multipliers.denied_models IS
    '该用户在此分组被禁用的模型（JSON 字符串数组，不区分大小写，支持末尾 * 通配）；NULL 表示不限制';
