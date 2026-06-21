-- name: CreateFriendship :one
INSERT INTO friendships (user_id, friend_id, status)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, friend_id) DO UPDATE SET deleted = FALSE, updated_at = NOW(), status = EXCLUDED.status
RETURNING *;

-- name: UpdateFriendshipStatus :one
UPDATE friendships SET status = $3
WHERE user_id = $1 AND friend_id = $2
RETURNING *;

-- name: DeleteFriendship :exec
UPDATE friendships SET deleted = TRUE, updated_at = NOW()
WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1);

-- name: GetFriendship :one
SELECT * FROM friendships
WHERE ((user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1))
  AND deleted = FALSE;

-- name: ListFriends :many
SELECT u.*,d.channel_id
 FROM users u
LEFT JOIN  dm_channel_members d on u.id = d.user_id
LEFT JOIN friendships f ON (f.friend_id = u.id AND f.user_id = $1) OR (f.user_id = u.id AND f.friend_id = $1)

WHERE f.status = 'accepted' AND f.deleted = FALSE;

-- name: ListPendingIncoming :many
SELECT *
 FROM friendships f 
JOIN users u ON u.id = f.user_id
WHERE f.friend_id = $1 AND f.status = 'pending' AND f.deleted = FALSE;

-- name: ListPendingOutgoing :many
SELECT *
 FROM friendships f
JOIN users u ON u.id = f.friend_id
WHERE f.user_id = $1 AND f.status = 'pending' AND f.deleted = FALSE;

-- name: ListBlocked :many
SELECT u.* 
FROM users u
JOIN friendships f ON f.friend_id = u.id
WHERE f.user_id = $1 AND f.status = 'blocked' AND f.deleted = FALSE;
