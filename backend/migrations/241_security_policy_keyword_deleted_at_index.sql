-- 241_security_policy_keyword_deleted_at_index.sql
-- 安全策略关键词软删过滤索引：与 ent schema (securitypolicykeyword_deleted_at) 对齐。
CREATE INDEX IF NOT EXISTS idx_secpol_kw_deleted_at ON security_policy_keywords(deleted_at);
