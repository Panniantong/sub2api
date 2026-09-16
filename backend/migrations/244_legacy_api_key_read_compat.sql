-- Restore the official plaintext key column on databases that previously
-- applied the custom hash-only migration. Existing hashes remain usable;
-- newly created keys follow the official storage path. No secrets are decrypted.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key VARCHAR(128);
ALTER TABLE api_keys ALTER COLUMN key DROP NOT NULL;
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_hash VARCHAR(64);
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS key_prefix VARCHAR(16);
CREATE UNIQUE INDEX IF NOT EXISTS api_keys_key_key ON api_keys(key);
