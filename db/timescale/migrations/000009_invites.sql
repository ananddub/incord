-- +goose Up
CREATE TABLE IF NOT EXISTS invites (
    code       VARCHAR(10) PRIMARY KEY,
    guild_id   UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    creator_id UUID NOT NULL REFERENCES users(id),
    max_uses   INTEGER NOT NULL DEFAULT 0,
    uses       INTEGER NOT NULL DEFAULT 0,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_invites_guild ON invites(guild_id);

-- +goose Down
DROP TABLE IF EXISTS invites;

