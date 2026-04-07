-- name: CreateMediaFile :one
INSERT INTO media_files (uploader_id, filename, content_type, size, bucket_key)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetMediaFile :one
SELECT * FROM media_files WHERE id = $1;

-- name: ConfirmMediaFile :one
UPDATE media_files SET confirmed = TRUE WHERE id = $1
RETURNING *;

-- name: DeleteMediaFile :exec
DELETE FROM media_files WHERE id = $1;
