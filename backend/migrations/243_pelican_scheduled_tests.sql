-- NULL configuration preserves existing connectivity tests.
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS pelican_config JSONB;
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS running_until TIMESTAMPTZ;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS pelican_config JSONB;
