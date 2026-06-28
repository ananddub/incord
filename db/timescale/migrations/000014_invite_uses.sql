-- +goose Up
CREATE TABLE IF NOT EXISTS invite_uses (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invite_code VARCHAR(10) NOT NULL REFERENCES invites(code) ON DELETE CASCADE,
    guild_id    UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    inviter_id  UUID NOT NULL REFERENCES users(id),
    used_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_invite_uses_guild ON invite_uses(guild_id);
CREATE INDEX IF NOT EXISTS idx_invite_uses_invite ON invite_uses(invite_code);

-- +goose Down
DROP TABLE IF EXISTS invite_uses;

