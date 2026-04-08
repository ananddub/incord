-- name: CreateChannel :one
INSERT INTO channels (guild_id, name, type, topic, position, parent_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetChannelByID :one
SELECT * FROM channels WHERE id = $1;

-- name: UpdateChannel :one
UPDATE channels SET
    name = COALESCE(sqlc.narg('name'), name),
    topic = COALESCE(sqlc.narg('topic'), topic),
    position = COALESCE(sqlc.narg('position'), position),
    parent_id = COALESCE(sqlc.narg('parent_id'), parent_id)
WHERE id = $1
RETURNING *;

-- name: DeleteChannel :exec
DELETE FROM channels WHERE id = $1;

-- name: ListGuildChannels :many
SELECT * FROM channels WHERE guild_id = $1 ORDER BY position;

-- name: CreateDMChannel :one
INSERT INTO channels (name, type)
VALUES ($1, $2)
RETURNING *;

-- name: AddDMChannelMember :exec
INSERT INTO dm_channel_members (channel_id, user_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: ListDMChannels :many
SELECT c.* FROM channels c
JOIN dm_channel_members dm ON dm.channel_id = c.id
WHERE dm.user_id = $1 AND c.type IN (5, 6);

-- name: GetDMChannelBetweenUsers :one
SELECT c.* FROM channels c
JOIN dm_channel_members dm1 ON dm1.channel_id = c.id AND dm1.user_id = $1
JOIN dm_channel_members dm2 ON dm2.channel_id = c.id AND dm2.user_id = $2
WHERE c.type = 5;

-- name: RemoveDMChannelMember :exec
DELETE FROM dm_channel_members WHERE channel_id = $1 AND user_id = $2;

-- name: IsDMChannelMember :one
SELECT EXISTS(
    SELECT 1 FROM dm_channel_members WHERE channel_id = $1 AND user_id = $2
) AS is_member;
