-- +goose Up
CREATE TABLE IF NOT EXISTS webhooks (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    guild_id   UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    name       VARCHAR(80) NOT NULL,
    avatar_url TEXT NOT NULL DEFAULT '',
    token      TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS webhooks;

