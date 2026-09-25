-- One quality rule owns account mutations, including while paused.
CREATE UNIQUE INDEX IF NOT EXISTS scheduled_test_quality_account_unique
 ON scheduled_test_plans(account_id) WHERE pelican_config->'quality' IS NOT NULL;
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS quality_action TEXT NOT NULL DEFAULT '';
CREATE TABLE IF NOT EXISTS account_quality_states (
 plan_id BIGINT PRIMARY KEY REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
 state JSONB NOT NULL DEFAULT '{}'
);
