CREATE KEYSPACE IF NOT EXISTS ndiscord
WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};

USE ndiscord;

-- Messages: partitioned by channel, clustered by time desc
CREATE TABLE IF NOT EXISTS messages (
    channel_id UUID,
    id         TIMEUUID,
    author_id  UUID,
    content    TEXT,
    type       INT,
    reply_to_id UUID,
    pinned     BOOLEAN,
    edited_at  TIMESTAMP,
    created_at TIMESTAMP,
    PRIMARY KEY (channel_id, id)
) WITH CLUSTERING ORDER BY (id DESC);

-- Message attachments
CREATE TABLE IF NOT EXISTS message_attachments (
    channel_id   UUID,
    message_id   TIMEUUID,
    id           UUID,
    filename     TEXT,
    url          TEXT,
    content_type TEXT,
    size         BIGINT,
    PRIMARY KEY ((channel_id, message_id), id)
);

-- Message reactions
CREATE TABLE IF NOT EXISTS message_reactions (
    channel_id UUID,
    message_id TIMEUUID,
    emoji      TEXT,
    user_id    UUID,
    PRIMARY KEY ((channel_id, message_id), emoji, user_id)
);

-- Read states: tracks last read message per user per channel
CREATE TABLE IF NOT EXISTS read_states (
    user_id             UUID,
    channel_id          UUID,
    last_read_message_id TIMEUUID,
    mention_count       INT,
    PRIMARY KEY (user_id, channel_id)
);

-- Audit log
CREATE TABLE IF NOT EXISTS audit_log (
    guild_id    UUID,
    id          TIMEUUID,
    action_type INT,
    user_id     UUID,
    target_id   UUID,
    changes     TEXT,
    created_at  TIMESTAMP,
    PRIMARY KEY (guild_id, id)
) WITH CLUSTERING ORDER BY (id DESC);
