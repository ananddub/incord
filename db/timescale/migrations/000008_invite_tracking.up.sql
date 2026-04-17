-- Track which invite each member used to join, and who invited them.
ALTER TABLE guild_members ADD COLUMN invite_code VARCHAR(10) DEFAULT NULL;
ALTER TABLE guild_members ADD COLUMN invited_by UUID DEFAULT NULL REFERENCES users(id);

-- Per-use invite log so the guild owner can see exactly when each person
-- joined via each invite code. guild_members only keeps the latest state;
-- this table is append-only history.
CREATE TABLE invite_uses (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invite_code VARCHAR(10) NOT NULL REFERENCES invites(code) ON DELETE CASCADE,
    guild_id   UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    inviter_id UUID NOT NULL REFERENCES users(id),
    used_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_invite_uses_guild ON invite_uses(guild_id);
CREATE INDEX idx_invite_uses_invite ON invite_uses(invite_code);
