-- Keep historical judgments intact; new evaluations separate execution and verdicts.
ALTER TABLE account_tests DROP CONSTRAINT IF EXISTS account_tests_status_check;
ALTER TABLE account_tests ADD CONSTRAINT account_tests_status_check CHECK (
  status IN ('queued','running','completed','cancelled','success','failed','rate_limited','account_error','model_error','request_error','network_error','suspected_degradation')
);
ALTER TABLE account_tests ADD COLUMN IF NOT EXISTS available_at TIMESTAMPTZ NOT NULL DEFAULT NOW();
ALTER TABLE account_tests ADD COLUMN IF NOT EXISTS queue_reason TEXT NOT NULL DEFAULT '';
UPDATE test_settings SET config = config || '{"answer_type":"number","answer_unit":"颗","answer_format":"answer_line"}'::jsonb
WHERE test_type='candy' AND config->>'evaluator'='exact_answer'
  AND config->>'expected_answer'='12'
  AND config->>'prompt' LIKE '盒子里有 24 颗糖。小明取走总数的四分之一%'
  AND NOT (config ? 'answer_type');
