-- +goose Up
CREATE TABLE IF NOT EXISTS dm_channel_members (
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    deleted    BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_dm_members_user ON dm_channel_members(user_id);

-- +goose Down
DROP TABLE IF EXISTS dm_channel_members;

