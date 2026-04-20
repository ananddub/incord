DROP TABLE IF EXISTS channel_permission_overwrites;

-- Restore the original bitmask shape from 000001_init so a rollback
-- leaves the table in the pre-000012 state.
CREATE TABLE channel_permission_overwrites (
    channel_id  UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    target_id   UUID NOT NULL,
    target_type VARCHAR(10) NOT NULL CHECK (target_type IN ('role','user')),
    allow_bits  BIGINT NOT NULL DEFAULT 0,
    deny_bits   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (channel_id, target_id)
);
