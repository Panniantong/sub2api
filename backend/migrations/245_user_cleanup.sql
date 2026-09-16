-- Additive only: existing users, credentials, balances and usage history remain intact.
CREATE TABLE IF NOT EXISTS user_cleanup_guards (
    user_id BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    is_system BOOLEAN NOT NULL DEFAULT FALSE,
    is_protected BOOLEAN NOT NULL DEFAULT FALSE,
    updated_by BIGINT,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- A preview is an immutable, operator-bound confirmation scope and idempotency key.
-- Completed results remain available for reconciliation after a connection failure.
CREATE TABLE IF NOT EXISTS user_cleanup_previews (
    id UUID PRIMARY KEY,
    actor_user_id BIGINT NOT NULL,
    candidate_ids BIGINT[] NOT NULL,
    candidate_snapshot JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '10 minutes'),
    completed_at TIMESTAMPTZ,
    result JSONB
);
CREATE INDEX IF NOT EXISTS idx_user_cleanup_previews_actor_created
    ON user_cleanup_previews (actor_user_id, created_at DESC);

-- Login bindings must be released just like the existing DeleteUser workflow,
-- otherwise canonical identity uniqueness prevents the same person registering again.
-- Preserve the original rows for internal recovery; no HTTP API exposes this archive.
CREATE TABLE IF NOT EXISTS user_cleanup_identity_archives (
    preview_id UUID NOT NULL REFERENCES user_cleanup_previews(id),
    user_id BIGINT NOT NULL,
    identities JSONB NOT NULL DEFAULT '[]'::jsonb,
    channels JSONB NOT NULL DEFAULT '[]'::jsonb,
    adoption_decisions JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (preview_id, user_id)
);

COMMENT ON TABLE user_cleanup_guards IS 'Explicit system/protected users excluded from inactivity cleanup';
COMMENT ON TABLE user_cleanup_previews IS 'Admin-confirmed bounded cleanup scope; soft deletion and audit commit atomically';
COMMENT ON TABLE user_cleanup_identity_archives IS 'Internal-only identity archive; never return through user/admin result APIs';
