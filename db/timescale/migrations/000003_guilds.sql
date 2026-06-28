-- +goose Up
CREATE TABLE IF NOT EXISTS guilds (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    icon_url    TEXT NOT NULL DEFAULT '',
    owner_id    UUID NOT NULL REFERENCES users(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted     BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    banner_url  TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE IF EXISTS guilds;

