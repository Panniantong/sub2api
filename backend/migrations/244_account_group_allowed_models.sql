-- 账号在单个分组内可用的模型清单。NULL 表示不限制，沿用账号自身支持的模型；
-- 旧版本二进制不读写该列，可继续运行。
ALTER TABLE account_groups
    ADD COLUMN IF NOT EXISTS allowed_models JSONB;

COMMENT ON COLUMN account_groups.allowed_models IS
    '账号在该分组内可用的模型（JSON 字符串数组，支持末尾 * 通配）；NULL 表示不限制，只能在账号自身支持的模型范围内收窄';
