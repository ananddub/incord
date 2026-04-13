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
