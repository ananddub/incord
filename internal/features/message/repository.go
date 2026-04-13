package message

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gocql/gocql"
)

type Repository struct {
	session *gocql.Session
}

func NewRepository(session *gocql.Session) *Repository {
	return &Repository{session: session}
}

func (r *Repository) CreateMessage(ctx context.Context, msg *Message) error {
	query := `INSERT INTO messages (channel_id, id, author_id, content, type, reply_to_id, pinned, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		msg.ChannelID,
		msg.ID,
		msg.AuthorID,
		msg.Content,
		msg.Type,
		msg.ReplyToID,
		msg.Pinned,
		msg.CreatedAt,
	).WithContext(ctx).Exec()
}

func (r *Repository) GetMessage(ctx context.Context, channelID, messageID gocql.UUID) (*Message, error) {
	query := `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at
		FROM messages WHERE channel_id = ? AND id = ? AND deleted = false`

	msg := &Message{}
	err := r.session.Query(query, channelID, messageID).WithContext(ctx).Scan(
		&msg.ChannelID,
		&msg.ID,
		&msg.AuthorID,
		&msg.Content,
		&msg.Type,
		&msg.ReplyToID,
		&msg.Pinned,
		&msg.EditedAt,
		&msg.CreatedAt,
		&msg.Deleted,
		&msg.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func (r *Repository) UpdateMessageContent(ctx context.Context, channelID, messageID gocql.UUID, content string, editedAt time.Time) error {
	query := `UPDATE messages SET content = ?, edited_at = ? WHERE channel_id = ? AND id = ?`
	return r.session.Query(query, content, editedAt, channelID, messageID).WithContext(ctx).Exec()
}

func (r *Repository) DeleteMessage(ctx context.Context, channelID, messageID gocql.UUID) error {
	query := `UPDATE messages SET deleted = true, updated_at = ? WHERE channel_id = ? AND id = ?`
	return r.session.Query(query, time.Now(), channelID, messageID).WithContext(ctx).Exec()
}

func (r *Repository) ListMessages(ctx context.Context, channelID gocql.UUID, before, after *gocql.UUID, limit int) ([]*Message, error) {
	var query string
	var args []any

	switch {
	case before != nil:
		query = `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at
			FROM messages WHERE channel_id = ? AND id < ? AND deleted = false ORDER BY id DESC LIMIT ?`
		args = []any{channelID, *before, limit}
	case after != nil:
		query = `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at
			FROM messages WHERE channel_id = ? AND id > ? AND deleted = false ORDER BY id ASC LIMIT ?`
		args = []any{channelID, *after, limit}
	default:
		query = `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at
			FROM messages WHERE channel_id = ? AND deleted = false ORDER BY id DESC LIMIT ?`
		args = []any{channelID, limit}
	}

	iter := r.session.Query(query, args...).WithContext(ctx).Iter()
	var messages []*Message
	for {
		msg := &Message{}
		if !iter.Scan(
			&msg.ChannelID,
			&msg.ID,
			&msg.AuthorID,
			&msg.Content,
			&msg.Type,
			&msg.ReplyToID,
			&msg.Pinned,
			&msg.EditedAt,
			&msg.CreatedAt,
			&msg.Deleted,
			&msg.UpdatedAt,
		) {
			break
		}
		messages = append(messages, msg)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to list messages: %w", err)
	}
	return messages, nil
}

func (r *Repository) SetPinned(ctx context.Context, channelID, messageID gocql.UUID, pinned bool) error {
	query := `UPDATE messages SET pinned = ? WHERE channel_id = ? AND id = ?`
	return r.session.Query(query, pinned, channelID, messageID).WithContext(ctx).Exec()
}

func (r *Repository) AddReaction(ctx context.Context, channelID, messageID gocql.UUID, emoji string, userID gocql.UUID) error {
	query := `INSERT INTO message_reactions (channel_id, message_id, emoji, user_id) VALUES (?, ?, ?, ?)`
	return r.session.Query(query, channelID, messageID, emoji, userID).WithContext(ctx).Exec()
}

func (r *Repository) RemoveReaction(ctx context.Context, channelID, messageID gocql.UUID, emoji string, userID gocql.UUID) error {
	query := `DELETE FROM message_reactions WHERE channel_id = ? AND message_id = ? AND emoji = ? AND user_id = ?`
	return r.session.Query(query, channelID, messageID, emoji, userID).WithContext(ctx).Exec()
}

func (r *Repository) GetReactions(ctx context.Context, channelID, messageID gocql.UUID, currentUserID gocql.UUID) ([]ReactionCount, error) {
	query := `SELECT emoji, user_id FROM message_reactions WHERE channel_id = ? AND message_id = ?`
	iter := r.session.Query(query, channelID, messageID).WithContext(ctx).Iter()

	// Aggregate: emoji -> set of user_ids
	emojiUsers := make(map[string][]gocql.UUID)
	var emoji string
	var uid gocql.UUID
	for iter.Scan(&emoji, &uid) {
		emojiUsers[emoji] = append(emojiUsers[emoji], uid)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to get reactions: %w", err)
	}

	var result []ReactionCount
	for e, users := range emojiUsers {
		me := false
		for _, u := range users {
			if u == currentUserID {
				me = true
				break
			}
		}
		result = append(result, ReactionCount{
			Emoji: e,
			Count: int32(len(users)),
			Me:    me,
		})
	}
	return result, nil
}

func (r *Repository) UpsertReadState(ctx context.Context, userID, channelID, messageID gocql.UUID) error {
	query := `INSERT INTO read_states (user_id, channel_id, last_read_message_id, mention_count)
		VALUES (?, ?, ?, 0)`
	return r.session.Query(query, userID, channelID, messageID).WithContext(ctx).Exec()
}

// GetUserReadStates returns all read states for a user.
func (r *Repository) GetUserReadStates(ctx context.Context, userID gocql.UUID) ([]ReadState, error) {
	query := `SELECT user_id, channel_id, last_read_message_id, mention_count FROM read_states WHERE user_id = ?`
	iter := r.session.Query(query, userID).WithContext(ctx).Iter()

	var states []ReadState
	var rs ReadState
	for iter.Scan(&rs.UserID, &rs.ChannelID, &rs.LastReadMessageID, &rs.MentionCount) {
		states = append(states, rs)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to get read states: %w", err)
	}
	return states, nil
}

// CountUnreadMessages counts messages in a channel after the given message ID.
func (r *Repository) CountUnreadMessages(ctx context.Context, channelID, afterMsgID gocql.UUID) (int, gocql.UUID, time.Time, error) {
	query := `SELECT id, created_at FROM messages WHERE channel_id = ? AND id > ? AND deleted = false ORDER BY id DESC`
	iter := r.session.Query(query, channelID, afterMsgID).WithContext(ctx).Iter()

	var count int
	var lastID gocql.UUID
	var lastTime time.Time
	var msgID gocql.UUID
	var createdAt time.Time

	for iter.Scan(&msgID, &createdAt) {
		count++
		if count == 1 {
			lastID = msgID
			lastTime = createdAt
		}
	}
	if err := iter.Close(); err != nil {
		return 0, gocql.UUID{}, time.Time{}, err
	}
	return count, lastID, lastTime, nil
}

// ListMessagesAfter returns up to `limit` messages after the given message ID (for unread previews).
func (r *Repository) ListMessagesAfter(ctx context.Context, channelID, afterMsgID gocql.UUID, limit int) ([]*Message, error) {
	query := `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at FROM messages WHERE channel_id = ? AND id > ? AND deleted = false ORDER BY id ASC LIMIT ?`
	iter := r.session.Query(query, channelID, afterMsgID, limit).WithContext(ctx).Iter()

	var results []*Message
	for {
		var m Message
		var editedAt *time.Time
		if !iter.Scan(&m.ChannelID, &m.ID, &m.AuthorID, &m.Content, &m.Type, &m.ReplyToID, &m.Pinned, &editedAt, &m.CreatedAt, &m.Deleted, &m.UpdatedAt) {
			break
		}
		if editedAt != nil {
			m.EditedAt = editedAt
		}
		results = append(results, &m)
	}
	iter.Close()
	return results, nil
}

// ListRecentMessages returns up to `limit` most recent messages in a channel.
func (r *Repository) ListRecentMessages(ctx context.Context, channelID gocql.UUID, limit int) ([]*Message, error) {
	query := `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at FROM messages WHERE channel_id = ? AND deleted = false ORDER BY id DESC LIMIT ?`
	iter := r.session.Query(query, channelID, limit).WithContext(ctx).Iter()

	var results []*Message
	for {
		var m Message
		var editedAt *time.Time
		if !iter.Scan(&m.ChannelID, &m.ID, &m.AuthorID, &m.Content, &m.Type, &m.ReplyToID, &m.Pinned, &editedAt, &m.CreatedAt, &m.Deleted, &m.UpdatedAt) {
			break
		}
		if editedAt != nil {
			m.EditedAt = editedAt
		}
		results = append(results, &m)
	}
	iter.Close()
	return results, nil
}

// CountAllMessages counts all messages in a channel (for channels with no read_state).
func (r *Repository) CountAllMessages(ctx context.Context, channelID gocql.UUID) (int, gocql.UUID, time.Time, error) {
	query := `SELECT id, created_at FROM messages WHERE channel_id = ? AND deleted = false ORDER BY id DESC`
	iter := r.session.Query(query, channelID).WithContext(ctx).Iter()

	var count int
	var lastID gocql.UUID
	var lastTime time.Time
	var msgID gocql.UUID
	var createdAt time.Time

	for iter.Scan(&msgID, &createdAt) {
		count++
		if count == 1 {
			lastID = msgID
			lastTime = createdAt
		}
	}
	if err := iter.Close(); err != nil {
		return 0, gocql.UUID{}, time.Time{}, err
	}
	return count, lastID, lastTime, nil
}

// SaveEditHistory stores old content before an edit.
func (r *Repository) SaveEditHistory(ctx context.Context, channelID, messageID gocql.UUID, oldContent string, editedAt time.Time) error {
	query := `INSERT INTO message_edit_history (channel_id, message_id, old_content, edited_at) VALUES (?, ?, ?, ?)`
	return r.session.Query(query, channelID, messageID, oldContent, editedAt).WithContext(ctx).Exec()
}

// GetEditHistory returns all previous versions of a message.
func (r *Repository) GetEditHistory(ctx context.Context, channelID, messageID gocql.UUID) ([]EditHistory, error) {
	query := `SELECT channel_id, message_id, old_content, edited_at FROM message_edit_history WHERE channel_id = ? AND message_id = ? ORDER BY edited_at DESC`
	iter := r.session.Query(query, channelID, messageID).WithContext(ctx).Iter()

	var edits []EditHistory
	var e EditHistory
	for iter.Scan(&e.ChannelID, &e.MessageID, &e.OldContent, &e.EditedAt) {
		edits = append(edits, e)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return edits, nil
}

// SearchMessages searches messages by content (ALLOW FILTERING - not ideal for production, use search index).
func (r *Repository) SearchMessages(ctx context.Context, channelID gocql.UUID, query string, limit int) ([]*Message, error) {
	// ScyllaDB doesn't support LIKE, so fetch recent and filter in Go
	cql := `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at FROM messages WHERE channel_id = ? AND deleted = false ORDER BY id DESC LIMIT ?`
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	iter := r.session.Query(cql, channelID, limit*5).WithContext(ctx).Iter() // fetch more to filter

	var results []*Message
	var m Message
	var editedAt *time.Time
	for iter.Scan(&m.ChannelID, &m.ID, &m.AuthorID, &m.Content, &m.Type, &m.ReplyToID, &m.Pinned, &editedAt, &m.CreatedAt, &m.Deleted, &m.UpdatedAt) {
		if editedAt != nil {
			t := *editedAt
			m.EditedAt = &t
		}
		if strings.Contains(strings.ToLower(m.Content), strings.ToLower(query)) {
			msg := m
			results = append(results, &msg)
			if len(results) >= limit {
				break
			}
		}
	}
	iter.Close()
	return results, nil
}

// GetThreadMessages returns all replies to a parent message.
func (r *Repository) GetThreadMessages(ctx context.Context, channelID, parentID gocql.UUID, limit int) ([]*Message, error) {
	// ScyllaDB: fetch recent messages and filter by reply_to_id
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	cql := `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at FROM messages WHERE channel_id = ? AND deleted = false ORDER BY id DESC LIMIT ?`
	iter := r.session.Query(cql, channelID, limit*10).WithContext(ctx).Iter()

	var results []*Message
	var m Message
	var editedAt *time.Time
	for iter.Scan(&m.ChannelID, &m.ID, &m.AuthorID, &m.Content, &m.Type, &m.ReplyToID, &m.Pinned, &editedAt, &m.CreatedAt, &m.Deleted, &m.UpdatedAt) {
		if editedAt != nil {
			t := *editedAt
			m.EditedAt = &t
		}
		if m.ReplyToID == parentID {
			msg := m
			results = append(results, &msg)
			if len(results) >= limit {
				break
			}
		}
	}
	iter.Close()
	return results, nil
}

// BulkDeleteMessages deletes multiple messages.
func (r *Repository) BulkDeleteMessages(ctx context.Context, channelID gocql.UUID, messageIDs []gocql.UUID) (int, error) {
	deleted := 0
	for _, msgID := range messageIDs {
		if err := r.DeleteMessage(ctx, channelID, msgID); err == nil {
			deleted++
		}
	}
	return deleted, nil
}

// GetMessagesSince returns all messages (including deleted) in a channel updated after the given time.
// Used for sync - returns deleted messages too so client knows what to remove.
func (r *Repository) GetMessagesSince(ctx context.Context, channelID gocql.UUID, since time.Time) ([]*Message, error) {
	query := `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, deleted, edited_at, updated_at, created_at
		FROM messages WHERE channel_id = ? AND updated_at > ? ALLOW FILTERING`
	iter := r.session.Query(query, channelID, since).WithContext(ctx).Iter()

	var results []*Message
	for {
		var m Message
		var editedAt, updatedAt *time.Time
		if !iter.Scan(&m.ChannelID, &m.ID, &m.AuthorID, &m.Content, &m.Type, &m.ReplyToID, &m.Pinned, &m.Deleted, &editedAt, &updatedAt, &m.CreatedAt) {
			break
		}
		m.EditedAt = editedAt
		m.UpdatedAt = updatedAt
		results = append(results, &m)
	}
	iter.Close()
	return results, nil
}
