-- name: CreateMessage :exec
INSERT INTO ndiscord.messages (
    channel_id,
    id,
    author_id,
    content,
    type,
    reply_to_id,
    pinned,
    created_at,
    forwarded_from_channel_id,
    forwarded_from_message_id,
    forwarded_from_author_id,
    mention_user_ids
)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetMessage :one
SELECT
    channel_id,
    id,
    author_id,
    content,
    type,
    reply_to_id,
    pinned,
    edited_at,
    created_at,
    deleted,
    updated_at,
    forwarded_from_channel_id,
    forwarded_from_message_id,
    forwarded_from_author_id,
    mention_user_ids
FROM ndiscord.messages
WHERE channel_id = ?
  AND id = ?;

-- name: UpdateMessageContent :exec
UPDATE ndiscord.messages
SET content = ?, edited_at = ?
WHERE channel_id = ? AND id = ?;

-- name: DeleteMessage :exec
UPDATE ndiscord.messages
SET deleted = true, updated_at = ?
WHERE channel_id = ? AND id = ?;

-- name: ListMessagesBefore :many
SELECT
    channel_id,
    id,
    author_id,
    content,
    type,
    reply_to_id,
    pinned,
    edited_at,
    created_at,
    deleted,
    updated_at,
    forwarded_from_channel_id,
    forwarded_from_message_id,
    forwarded_from_author_id,
    mention_user_ids
FROM ndiscord.messages
WHERE channel_id = ?
  AND id < ?
ORDER BY id DESC
LIMIT ?;

-- name: ListMessagesAfter :many
SELECT
    channel_id,
    id,
    author_id,
    content,
    type,
    reply_to_id,
    pinned,
    edited_at,
    created_at,
    deleted,
    updated_at,
    forwarded_from_channel_id,
    forwarded_from_message_id,
    forwarded_from_author_id,
    mention_user_ids
FROM ndiscord.messages
WHERE channel_id = ?
  AND id > ?
ORDER BY id ASC
LIMIT ?;

-- name: ListRecentMessages :many
SELECT
    channel_id,
    id,
    author_id,
    content,
    type,
    reply_to_id,
    pinned,
    edited_at,
    created_at,
    deleted,
    updated_at,
    forwarded_from_channel_id,
    forwarded_from_message_id,
    forwarded_from_author_id,
    mention_user_ids
FROM ndiscord.messages
WHERE channel_id = ?
ORDER BY id DESC
LIMIT ?;

-- name: SetPinned :exec
UPDATE ndiscord.messages
SET pinned = ?
WHERE channel_id = ?
  AND id = ?;

-- name: GetMessagesSince :many
SELECT
    channel_id,
    id,
    author_id,
    content,
    type,
    reply_to_id,
    pinned,
    edited_at,
    created_at,
    deleted,
    updated_at,
    forwarded_from_channel_id,
    forwarded_from_message_id,
    forwarded_from_author_id,
    mention_user_ids
FROM ndiscord.messages
WHERE channel_id = ?
  AND updated_at > ?
ALLOW FILTERING;

-- name: AddMessageAttachment :exec
INSERT INTO ndiscord.message_attachments (
    channel_id,
    message_id,
    id,
    filename,
    url,
    content_type,
    size
)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: GetMessageAttachments :many
SELECT
    id,
    filename,
    url,
    content_type,
    size
FROM ndiscord.message_attachments
WHERE channel_id = ?
  AND message_id = ?;

-- name: DeleteMessageAttachments :exec
DELETE FROM ndiscord.message_attachments
WHERE channel_id = ?
  AND message_id = ?;

-- name: AddReaction :exec
INSERT INTO ndiscord.message_reactions (
    channel_id,
    message_id,
    emoji,
    user_id
)
VALUES (?, ?, ?, ?);

-- name: RemoveReaction :exec
DELETE FROM ndiscord.message_reactions
WHERE channel_id = ?
  AND message_id = ?
  AND emoji = ?
  AND user_id = ?;

-- name: GetReactions :many
SELECT
    emoji,
    user_id
FROM ndiscord.message_reactions
WHERE channel_id = ?
  AND message_id = ?;

-- name: DeleteMessageReactions :exec
DELETE FROM ndiscord.message_reactions
WHERE channel_id = ?
  AND message_id = ?;

-- name: GetReadState :one
SELECT
    mention_count,
    last_read_message_id
FROM ndiscord.read_states
WHERE user_id = ?
  AND channel_id = ?;

-- name: UpsertReadState :exec
INSERT INTO ndiscord.read_states (
    user_id,
    channel_id,
    last_read_message_id,
    mention_count
)
VALUES (?, ?, ?, ?);

-- name: IncrementMentionCount :exec
INSERT INTO ndiscord.read_states (
    user_id,
    channel_id,
    last_read_message_id,
    mention_count
)
VALUES (?, ?, ?, ?);

-- name: GetUserReadStates :many
SELECT
    user_id,
    channel_id,
    last_read_message_id,
    mention_count
FROM ndiscord.read_states
WHERE user_id = ?;

-- name: CountUnreadMessages :many
SELECT
    id,
    created_at,
    deleted
FROM ndiscord.messages
WHERE channel_id = ?
  AND id > ?
ORDER BY id DESC;

-- name: CountAllMessages :many
SELECT
    id,
    created_at,
    deleted
FROM ndiscord.messages
WHERE channel_id = ?
ORDER BY id DESC;

-- name: SaveEditHistory :exec
INSERT INTO ndiscord.message_edit_history (
    channel_id,
    message_id,
    old_content,
    edited_at
)
VALUES (?, ?, ?, ?);

-- name: GetEditHistory :many
SELECT
    channel_id,
    message_id,
    old_content,
    edited_at
FROM ndiscord.message_edit_history
WHERE channel_id = ?
  AND message_id = ?
ORDER BY edited_at DESC;

-- name: DeleteEditHistory :exec
DELETE FROM ndiscord.message_edit_history
WHERE channel_id = ?
  AND message_id = ?;

-- name: SearchMessagesSource :many
SELECT
    channel_id,
    id,
    author_id,
    content,
    type,
    reply_to_id,
    pinned,
    edited_at,
    created_at,
    deleted,
    updated_at,
    forwarded_from_channel_id,
    forwarded_from_message_id,
    forwarded_from_author_id,
    mention_user_ids
FROM ndiscord.messages
WHERE channel_id = ?
ORDER BY id DESC
LIMIT ?;

-- name: GetThreadMessagesSource :many
SELECT
    channel_id,
    id,
    author_id,
    content,
    type,
    reply_to_id,
    pinned,
    edited_at,
    created_at,
    deleted,
    updated_at,
    forwarded_from_channel_id,
    forwarded_from_message_id,
    forwarded_from_author_id,
    mention_user_ids
FROM ndiscord.messages
WHERE channel_id = ?
ORDER BY id DESC
LIMIT ?;

-- name: DeleteMessageAttachmentsCascade :exec
DELETE FROM ndiscord.message_attachments
WHERE channel_id = ?
  AND message_id = ?;

-- name: DeleteMessageReactionsCascade :exec
DELETE FROM ndiscord.message_reactions
WHERE channel_id = ?
  AND message_id = ?;

-- name: DeleteMessageEditHistoryCascade :exec
DELETE FROM ndiscord.message_edit_history
WHERE channel_id = ?
  AND message_id = ?;
