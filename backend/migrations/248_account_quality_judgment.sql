ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS quality_judgment JSONB;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS quality_round_id TEXT NOT NULL DEFAULT '';
