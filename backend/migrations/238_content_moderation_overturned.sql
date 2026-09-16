-- 238_content_moderation_overturned.sql
-- 安全策略模型复核：复核推翻本地命中时标记 overturned 并解封会话。

ALTER TABLE content_moderation_logs ADD COLUMN IF NOT EXISTS overturned BOOLEAN NOT NULL DEFAULT FALSE;
