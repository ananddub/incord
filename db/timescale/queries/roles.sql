-- name: CreateRole :one
INSERT INTO roles (guild_id, name, color, position)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRoleByID :one
SELECT * FROM roles WHERE id = $1 AND deleted = FALSE;

-- name: UpdateRole :one
UPDATE roles SET
    name = COALESCE(sqlc.narg('name'), name),
    color = COALESCE(sqlc.narg('color'), color),
    position = COALESCE(sqlc.narg('position'), position)
WHERE id = $1
RETURNING *;

-- name: DeleteRole :exec
UPDATE roles SET deleted = TRUE, updated_at = NOW() WHERE id = $1;

-- name: ListGuildRoles :many
SELECT * FROM roles WHERE guild_id = $1 AND deleted = FALSE ORDER BY position;

-- name: AssignRole :exec
INSERT INTO role_members (role_id, user_id) VALUES ($1, $2)
ON CONFLICT (role_id, user_id) DO UPDATE SET deleted = FALSE, updated_at = NOW();

-- name: RemoveRole :exec
UPDATE role_members SET deleted = TRUE, updated_at = NOW() WHERE role_id = $1 AND user_id = $2;

-- name: ListUserRoles :many
SELECT r.* FROM roles r
JOIN role_members rm ON rm.role_id = r.id
WHERE rm.user_id = $1 AND r.guild_id = $2 AND rm.deleted = FALSE AND r.deleted = FALSE;

-- name: GetEveryoneRole :one
-- @everyone is the implicit baseline role every guild member belongs to.
-- By convention it is stored as the row named "@everyone" at position 0.
-- Auto-created on guild creation; all member-add paths assign to it.
SELECT * FROM roles
WHERE guild_id = $1 AND name = '@everyone' AND deleted = FALSE
LIMIT 1;

-- name: ListEveryoneAssignments :many
-- Every active member of every guild paired with the guild's @everyone
-- role id. Consumed by the startup sync that writes matching OpenFGA
-- tuples, so OpenFGA stays in step with Postgres after a deploy that
-- introduces @everyone for the first time.
SELECT
    r.guild_id AS guild_id,
    r.id       AS role_id,
    gm.user_id AS user_id
FROM roles r
JOIN guild_members gm ON gm.guild_id = r.guild_id
WHERE r.name = '@everyone'
  AND r.deleted = FALSE
  AND gm.deleted = FALSE;
