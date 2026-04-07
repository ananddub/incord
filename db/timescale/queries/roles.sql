-- name: CreateRole :one
INSERT INTO roles (guild_id, name, color, position, permissions)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetRoleByID :one
SELECT * FROM roles WHERE id = $1;

-- name: UpdateRole :one
UPDATE roles SET
    name = COALESCE(sqlc.narg('name'), name),
    color = COALESCE(sqlc.narg('color'), color),
    permissions = COALESCE(sqlc.narg('permissions'), permissions),
    position = COALESCE(sqlc.narg('position'), position)
WHERE id = $1
RETURNING *;

-- name: DeleteRole :exec
DELETE FROM roles WHERE id = $1;

-- name: ListGuildRoles :many
SELECT * FROM roles WHERE guild_id = $1 ORDER BY position;

-- name: AssignRole :exec
INSERT INTO role_members (role_id, user_id) VALUES ($1, $2)
ON CONFLICT DO NOTHING;

-- name: RemoveRole :exec
DELETE FROM role_members WHERE role_id = $1 AND user_id = $2;

-- name: ListUserRoles :many
SELECT r.* FROM roles r
JOIN role_members rm ON rm.role_id = r.id
WHERE rm.user_id = $1 AND r.guild_id = $2;
