-- +goose Up
CREATE TABLE IF NOT EXISTS channels (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    guild_id   UUID REFERENCES guilds(id) ON DELETE CASCADE,
    name       VARCHAR(100) NOT NULL,
    type       INTEGER NOT NULL DEFAULT 1,
    topic      TEXT NOT NULL DEFAULT '',
    position   INTEGER NOT NULL DEFAULT 0,
    parent_id  UUID REFERENCES channels(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_channels_guild ON channels(guild_id);
CREATE INDEX IF NOT EXISTS idx_channels_parent ON channels(parent_id);

-- +goose Down
DROP TABLE IF EXISTS channels;

