-- 分组「仅允许流式请求」开关：开启后 Messages / Chat Completions / Responses 的非流式请求
-- 与 Gemini generateContent 在网关入口直接拒绝；默认关闭，行为与之前一致。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS stream_only BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN groups.stream_only IS '是否仅允许流式请求：开启后非流式的对话生成请求在网关入口直接拒绝';
