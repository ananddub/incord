-- name: CreateMediaFile :one
INSERT INTO media_files (uploader_id, filename, content_type, size, bucket_key)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetMediaFile :one
SELECT * FROM media_files WHERE id = $1 AND deleted = FALSE;

-- name: ConfirmMediaFile :one
UPDATE media_files SET confirmed = TRUE WHERE id = $1
RETURNING *;

-- name: DeleteMediaFile :exec
UPDATE media_files SET deleted = TRUE, updated_at = NOW() WHERE id = $1;

