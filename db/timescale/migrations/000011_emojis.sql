-- +goose Up
CREATE TABLE IF NOT EXISTS emojis (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    guild_id   UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    name       VARCHAR(32) NOT NULL,
    image_url  TEXT NOT NULL,
    creator_id UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS emojis;

