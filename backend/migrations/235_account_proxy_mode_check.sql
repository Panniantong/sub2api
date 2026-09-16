-- 235_account_proxy_mode_check.sql
-- proxy_mode 白名单：只允许缺失或 'random'。先归一化历史脏数据，再加约束。

-- 1. 删除非法值（非字符串，或 trim/lower 后不是 random 的）。
UPDATE accounts
SET extra = COALESCE(extra, '{}'::jsonb) - 'proxy_mode'
WHERE extra IS NOT NULL
  AND (extra ? 'proxy_mode')
  AND NOT (
    jsonb_typeof(extra->'proxy_mode') = 'string'
    AND lower(btrim(extra->>'proxy_mode')) = 'random'
  );

-- 2. 归一化大小写/空白（如 ' Random ' -> 'random'）。
UPDATE accounts
SET extra = jsonb_set(COALESCE(extra, '{}'::jsonb), '{proxy_mode}', '"random"'::jsonb, false)
WHERE extra IS NOT NULL
  AND (extra ? 'proxy_mode')
  AND extra->>'proxy_mode' <> 'random';

-- 幂等加约束（与 154_account_spark_shadow.sql 同一写法）。
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_accounts_extra_proxy_mode') THEN
    ALTER TABLE accounts ADD CONSTRAINT chk_accounts_extra_proxy_mode
      CHECK (
        extra IS NULL
        OR NOT (extra ? 'proxy_mode')
        OR lower(btrim(extra->>'proxy_mode')) = 'random'
      ) NOT VALID;
  END IF;
END $$;

ALTER TABLE accounts VALIDATE CONSTRAINT chk_accounts_extra_proxy_mode;
