-- Host cooldowns and rotation history survive account deletion and reauthorization.
CREATE OR REPLACE FUNCTION cookie_account_email(credentials jsonb, extra jsonb, name text)
RETURNS text LANGUAGE sql IMMUTABLE AS $$
 SELECT lower(btrim(CASE
 WHEN position('@' in COALESCE(credentials->>'email',''))>0 THEN credentials->>'email'
 WHEN position('@' in COALESCE(extra->>'email',''))>0 THEN extra->>'email'
 WHEN position('@' in COALESCE(name,''))>0 THEN name ELSE '' END))
$$;

CREATE TABLE IF NOT EXISTS cookie_email_host_cooldowns (
 email text NOT NULL,
 host text NOT NULL,
 cooldown_until timestamptz NOT NULL,
 PRIMARY KEY(email,host)
);

-- New records carry mailbox identity; legacy history is intentionally not backfilled.
ALTER TABLE cookie_validation_history ADD COLUMN IF NOT EXISTS account_email text NOT NULL DEFAULT '';
