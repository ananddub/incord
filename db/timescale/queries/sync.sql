-- Sync queries: get all rows updated after a given timestamp for a user

-- name: SyncUsers :many
-- Get users that changed (friends + guild members of user's guilds)
SELECT DISTINCT u.*
 FROM users u
WHERE u.updated_at > $1
AND (
  -- User's own profile
  u.id = $2
  -- Friends
  OR u.id IN (SELECT friend_id FROM friendships WHERE user_id = $2 AND deleted = FALSE)
  OR u.id IN (SELECT user_id FROM friendships WHERE friend_id = $2 AND deleted = FALSE)
  -- Guild members
  OR u.id IN (
    SELECT gm2.user_id FROM guild_members gm2
    WHERE gm2.guild_id IN (SELECT gm.guild_id FROM guild_members gm WHERE gm.user_id = $2 AND gm.deleted = FALSE)
  )
);

-- name: SyncGuilds :many
SELECT g.* FROM guilds g
JOIN guild_members gm ON gm.guild_id = g.id
WHERE gm.user_id = $1 AND gm.deleted = FALSE
AND g.updated_at > $2;

-- name: SyncChannels :many
SELECT c.* FROM channels c
WHERE c.updated_at > $2
AND (
  -- Guild channels
  c.guild_id IN (SELECT gm.guild_id FROM guild_members gm WHERE gm.user_id = $1 AND gm.deleted = FALSE)
  -- DM channels
  OR c.id IN (SELECT dm.channel_id FROM dm_channel_members dm WHERE dm.user_id = $1 AND dm.deleted = FALSE)
);

-- name: SyncRoles :many
SELECT r.* FROM roles r
WHERE r.updated_at > $2
AND r.guild_id IN (SELECT gm.guild_id FROM guild_members gm WHERE gm.user_id = $1 AND gm.deleted = FALSE);

-- name: SyncGuildMembers :many
SELECT gm.* FROM guild_members gm
WHERE gm.updated_at > $2
AND gm.guild_id IN (SELECT gm2.guild_id FROM guild_members gm2 WHERE gm2.user_id = $1 AND gm2.deleted = FALSE);

-- name: SyncFriendships :many
SELECT
    f.*,
    u.*,
  d.channel_id
FROM friendships f
LEFT JOIN dm_channel_members d on f.friend_id = d.user_id
LEFT JOIN users u
    ON u.id = CASE
        WHEN f.user_id = $1 THEN f.friend_id
        ELSE f.user_id
    END
WHERE
    (f.user_id = $1 OR f.friend_id = $1);
-- AND f.updated_at > $2;

-- name: SyncDmMembers :many
SELECT dm.* FROM dm_channel_members dm
WHERE dm.updated_at > $2
AND dm.channel_id IN (SELECT dm2.channel_id FROM dm_channel_members dm2 WHERE dm2.user_id = $1 AND dm2.deleted = FALSE);
