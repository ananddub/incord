package message

import (
	"context"
	"fmt"
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
	query := `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at
		FROM messages WHERE channel_id = ? AND id = ?`

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
	query := `DELETE FROM messages WHERE channel_id = ? AND id = ?`
	return r.session.Query(query, channelID, messageID).WithContext(ctx).Exec()
}

func (r *Repository) ListMessages(ctx context.Context, channelID gocql.UUID, before, after *gocql.UUID, limit int) ([]*Message, error) {
	var query string
	var args []any

	switch {
	case before != nil:
		query = `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at
			FROM messages WHERE channel_id = ? AND id < ? ORDER BY id DESC LIMIT ?`
		args = []any{channelID, *before, limit}
	case after != nil:
		query = `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at
			FROM messages WHERE channel_id = ? AND id > ? ORDER BY id ASC LIMIT ?`
		args = []any{channelID, *after, limit}
	default:
		query = `SELECT channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at
			FROM messages WHERE channel_id = ? ORDER BY id DESC LIMIT ?`
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
	query := `SELECT id, created_at FROM messages WHERE channel_id = ? AND id > ? ORDER BY id DESC`
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
