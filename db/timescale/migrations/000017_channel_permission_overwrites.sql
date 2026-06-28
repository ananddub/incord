-- +goose Up
CREATE TABLE IF NOT EXISTS channel_permission_overwrites (
    channel_id    UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    target_type   VARCHAR(10) NOT NULL CHECK (target_type IN ('role', 'user')),
    target_id     UUID NOT NULL,
    permission_id BIGINT NOT NULL REFERENCES permissions(id) ON DELETE RESTRICT,
    effect        VARCHAR(5) NOT NULL CHECK (effect IN ('allow', 'deny')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, target_type, target_id, permission_id)
);

CREATE INDEX IF NOT EXISTS idx_cpo_channel ON channel_permission_overwrites(channel_id);
CREATE INDEX IF NOT EXISTS idx_cpo_target ON channel_permission_overwrites(target_type, target_id);

-- +goose Down
DROP TABLE IF EXISTS channel_permission_overwrites;

