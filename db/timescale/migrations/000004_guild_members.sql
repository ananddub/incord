-- +goose Up
CREATE TABLE IF NOT EXISTS guild_members (
    guild_id    UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    nickname    VARCHAR(32) NOT NULL DEFAULT '',
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted     BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    invite_code VARCHAR(10) DEFAULT NULL,
    invited_by  UUID REFERENCES users(id),
    PRIMARY KEY (guild_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_guild_members_user ON guild_members(user_id);
CREATE INDEX IF NOT EXISTS idx_guild_members_guild ON guild_members(guild_id);

-- +goose Down
DROP TABLE IF EXISTS guild_members;

