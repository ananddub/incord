-- name: CreateUser :one
INSERT INTO users (username, email, password_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1 AND deleted = FALSE;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = $1 AND deleted = FALSE;

-- name: UpdateUser :one
UPDATE users SET
    username = COALESCE(sqlc.narg('username'), username),
    avatar_url = COALESCE(sqlc.narg('avatar_url'), avatar_url),
    bio = COALESCE(sqlc.narg('bio'), bio),
    status = COALESCE(sqlc.narg('status'), status),
    background_color = COALESCE(sqlc.narg('background_color'), background_color),
    updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: DeleteUser :exec
UPDATE users SET deleted = TRUE, updated_at = NOW() WHERE id = $1;

-- name: SearchUsers :many
SELECT * FROM users
WHERE username ILIKE '%' || $1 || '%'
  AND deleted = FALSE
ORDER BY username
LIMIT $2 OFFSET $3;

-- name: CountSearchUsers :one
SELECT COUNT(*) FROM users
WHERE username ILIKE '%' || $1 || '%'
  AND deleted = FALSE;

-- name: VerifyUser :one
UPDATE users SET verified = TRUE, updated_at = NOW()
WHERE id = $1
RETURNING *;

-- name: IsUserVerified :one
SELECT verified FROM users WHERE email = $1 AND deleted = FALSE;
