-- Re-shape channel_permission_overwrites to align with the RBAC schema
-- added in 000011: one row per (channel, target, permission) with an
-- explicit `effect` of 'allow' or 'deny'. The old `allow_bits` / `deny_bits`
-- columns were a bitmask that never got wired up from code — no data
-- to migrate — so a straight drop-and-recreate keeps the schema clean.

DROP TABLE IF EXISTS channel_permission_overwrites;

CREATE TABLE channel_permission_overwrites (
    channel_id    UUID        NOT NULL REFERENCES channels(id)    ON DELETE CASCADE,
    target_type   VARCHAR(10) NOT NULL CHECK (target_type IN ('role','user')),
    target_id     UUID        NOT NULL,
    permission_id BIGINT      NOT NULL REFERENCES permissions(id) ON DELETE RESTRICT,
    effect        VARCHAR(5)  NOT NULL CHECK (effect IN ('allow','deny')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (channel_id, target_type, target_id, permission_id)
);

-- One index per common read path:
--   resolve all overrides for a channel (rendering the channel's settings page)
--   resolve all overrides targeting a given role or user
CREATE INDEX idx_cpo_channel ON channel_permission_overwrites(channel_id);
CREATE INDEX idx_cpo_target  ON channel_permission_overwrites(target_type, target_id);
