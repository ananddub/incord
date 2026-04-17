-- name: CreateGuild :one
INSERT INTO guilds (name, description, icon_url, owner_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetGuildByID :one
SELECT * FROM guilds WHERE id = $1 AND deleted = FALSE;

-- name: UpdateGuild :one
UPDATE guilds SET
    name = COALESCE(sqlc.narg('name'), name),
    description = COALESCE(sqlc.narg('description'), description),
    icon_url = COALESCE(sqlc.narg('icon_url'), icon_url)
WHERE id = $1
RETURNING *;

-- name: DeleteGuild :exec
UPDATE guilds SET deleted = TRUE, updated_at = NOW() WHERE id = $1;

-- name: ListUserGuilds :many
SELECT g.* FROM guilds g
JOIN guild_members gm ON gm.guild_id = g.id
WHERE gm.user_id = $1 AND gm.deleted = FALSE AND g.deleted = FALSE;

-- name: AddGuildMember :one
INSERT INTO guild_members (guild_id, user_id, nickname, invite_code, invited_by)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (guild_id, user_id) DO UPDATE SET deleted = FALSE, updated_at = NOW(), nickname = EXCLUDED.nickname, invite_code = EXCLUDED.invite_code, invited_by = EXCLUDED.invited_by
RETURNING *;

-- name: RemoveGuildMember :exec
UPDATE guild_members SET deleted = TRUE, updated_at = NOW() WHERE guild_id = $1 AND user_id = $2;

-- name: GetGuildMember :one
SELECT * FROM guild_members WHERE guild_id = $1 AND user_id = $2 AND deleted = FALSE;

-- name: ListGuildMembers :many
SELECT * FROM guild_members
WHERE guild_id = $1 AND deleted = FALSE
ORDER BY joined_at
LIMIT $2 OFFSET $3;

-- name: CountGuildMembers :one
SELECT COUNT(*) FROM guild_members WHERE guild_id = $1 AND deleted = FALSE;

-- name: CreateBan :one
INSERT INTO bans (guild_id, user_id, reason)
VALUES ($1, $2, $3)
ON CONFLICT (guild_id, user_id) DO UPDATE SET deleted = FALSE, updated_at = NOW(), reason = EXCLUDED.reason
RETURNING *;

-- name: DeleteBan :exec
UPDATE bans SET deleted = TRUE, updated_at = NOW() WHERE guild_id = $1 AND user_id = $2;

-- name: GetBan :one
SELECT * FROM bans WHERE guild_id = $1 AND user_id = $2 AND deleted = FALSE;

-- name: CreateInvite :one
INSERT INTO invites (code, guild_id, channel_id, creator_id, max_uses, expires_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetInvite :one
SELECT * FROM invites WHERE code = $1 AND deleted = FALSE;

-- name: IncrementInviteUses :exec
UPDATE invites SET uses = uses + 1 WHERE code = $1;

-- name: TransferGuildOwnership :one
UPDATE guilds SET owner_id = $2 WHERE id = $1
RETURNING *;

-- name: DeleteInvite :exec
UPDATE invites SET deleted = TRUE, updated_at = NOW() WHERE code = $1;

-- name: RecordInviteUse :one
INSERT INTO invite_uses (invite_code, guild_id, user_id, inviter_id)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListInviteUses :many
SELECT iu.*, u.username, u.display_name, u.avatar_url
FROM invite_uses iu
JOIN users u ON u.id = iu.user_id
WHERE iu.invite_code = $1
ORDER BY iu.used_at DESC
LIMIT $2 OFFSET $3;

-- name: ListGuildInviteUses :many
SELECT iu.*, u.username, u.display_name, u.avatar_url
FROM invite_uses iu
JOIN users u ON u.id = iu.user_id
WHERE iu.guild_id = $1
ORDER BY iu.used_at DESC
LIMIT $2 OFFSET $3;

-- name: CountInviteUsesByCode :one
SELECT COUNT(*) FROM invite_uses WHERE invite_code = $1;
