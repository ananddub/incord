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
	query := `INSERT INTO messages (channel_id, id, author_id, content, type, reply_to_id, pinned, created_at, forwarded_from_channel_id, forwarded_from_message_id, forwarded_from_author_id, mention_user_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query,
		msg.ChannelID,
		msg.ID,
		msg.AuthorID,
		msg.Content,
		msg.Type,
		msg.ReplyToID,
		msg.Pinned,
		msg.CreatedAt,
		msg.ForwardedFromChannelID,
		msg.ForwardedFromMessageID,
		msg.ForwardedFromAuthorID,
		msg.MentionUserIDs,
	).WithContext(ctx).Exec()
}

// messageColumns lists the SELECT projection used by GetMessage and
// ListMessages-style queries so they stay in sync.
const messageColumns = `channel_id, id, author_id, content, type, reply_to_id, pinned, edited_at, created_at, deleted, updated_at, forwarded_from_channel_id, forwarded_from_message_id, forwarded_from_author_id, mention_user_ids`

// scanMessage scans the messageColumns projection in order from a single
// row or iterator step into a Message struct. Returns whether the scan
// succeeded so it composes nicely with iter.Scan loops.
func scanMessage(scanner interface {
	Scan(dest ...any) bool
}, msg *Message) bool {
	return scanner.Scan(
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
		&msg.ForwardedFromChannelID,
		&msg.ForwardedFromMessageID,
		&msg.ForwardedFromAuthorID,
		&msg.MentionUserIDs,
	)
}

func (r *Repository) GetMessage(ctx context.Context, channelID, messageID gocql.UUID) (*Message, error) {
	// Scylla refuses equality on non-PK columns, so we can't filter
	// `deleted = false` server-side — we fetch and skip in Go.
	query := `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? AND id = ?`

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
		&msg.ForwardedFromChannelID,
		&msg.ForwardedFromMessageID,
		&msg.ForwardedFromAuthorID,
		&msg.MentionUserIDs,
	)
	if err != nil {
		return nil, err
	}
	if msg.Deleted {
		return nil, gocql.ErrNotFound
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

// IncrementMentionCount bumps the mention_count for a user's read_state in
// the given channel, creating the row with count=1 if it doesn't yet exist.
// Scylla doesn't support `UPDATE … SET col = col + 1` for non-counter cols,
// so we emulate it with a read-modify-write. Race-y but acceptable for a
// best-effort badge counter.
func (r *Repository) IncrementMentionCount(ctx context.Context, userID, channelID gocql.UUID) error {
	var current int
	var lastRead gocql.UUID
	err := r.session.Query(
		`SELECT mention_count, last_read_message_id FROM read_states WHERE user_id = ? AND channel_id = ?`,
		userID, channelID,
	).WithContext(ctx).Scan(&current, &lastRead)
	if err != nil && err != gocql.ErrNotFound {
		return err
	}
	current++
	return r.session.Query(
		`INSERT INTO read_states (user_id, channel_id, last_read_message_id, mention_count) VALUES (?, ?, ?, ?)`,
		userID, channelID, lastRead, current,
	).WithContext(ctx).Exec()
}

// CascadeDeleteChildren removes attachments, reactions and edit history
// belonging to a soft-deleted message. We keep the (now-deleted) messages
// row itself as a tombstone for sync, but children carry no value once the
// parent is gone and can be hard-deleted.
func (r *Repository) CascadeDeleteChildren(ctx context.Context, channelID, messageID gocql.UUID) error {
	stmts := []string{
		`DELETE FROM message_attachments WHERE channel_id = ? AND message_id = ?`,
		`DELETE FROM message_reactions WHERE channel_id = ? AND message_id = ?`,
		`DELETE FROM message_edit_history WHERE channel_id = ? AND message_id = ?`,
	}
	for _, q := range stmts {
		if err := r.session.Query(q, channelID, messageID).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("cascade delete: %w", err)
		}
	}
	return nil
}

func (r *Repository) ListMessages(ctx context.Context, channelID gocql.UUID, before, after *gocql.UUID, limit int) ([]*Message, error) {
	var query string
	var args []any

	// `deleted` is not part of the PK and Scylla refuses equality filters on
	// non-PK columns, so we skip soft-deleted rows in Go. To keep the
	// effective page size close to `limit`, we over-fetch a little.
	fetchLimit := limit * 2
	if fetchLimit < limit {
		fetchLimit = limit
	}
	switch {
	case before != nil:
		query = `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? AND id < ? ORDER BY id DESC LIMIT ?`
		args = []any{channelID, *before, fetchLimit}
	case after != nil:
		query = `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? AND id > ? ORDER BY id ASC LIMIT ?`
		args = []any{channelID, *after, fetchLimit}
	default:
		query = `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? ORDER BY id DESC LIMIT ?`
		args = []any{channelID, fetchLimit}
	}

	iter := r.session.Query(query, args...).WithContext(ctx).Iter()
	var messages []*Message
	for {
		msg := &Message{}
		if !scanMessage(iter, msg) {
			break
		}
		if msg.Deleted {
			continue
		}
		messages = append(messages, msg)
		if len(messages) >= limit {
			break
		}
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

// AddMessageAttachment persists a single attachment row for a message.
// `id` is the attachment id (distinct from media_file id), stored so clients
// can reference individual attachments.
func (r *Repository) AddMessageAttachment(ctx context.Context, channelID, messageID, attachmentID gocql.UUID, filename, urlStr, contentType string, size int64) error {
	query := `INSERT INTO message_attachments (channel_id, message_id, id, filename, url, content_type, size) VALUES (?, ?, ?, ?, ?, ?, ?)`
	return r.session.Query(query, channelID, messageID, attachmentID, filename, urlStr, contentType, size).WithContext(ctx).Exec()
}

// GetMessageAttachments loads all attachments for the given message.
func (r *Repository) GetMessageAttachments(ctx context.Context, channelID, messageID gocql.UUID) ([]Attachment, error) {
	query := `SELECT id, filename, url, content_type, size FROM message_attachments WHERE channel_id = ? AND message_id = ?`
	iter := r.session.Query(query, channelID, messageID).WithContext(ctx).Iter()
	var out []Attachment
	var a Attachment
	for iter.Scan(&a.ID, &a.Filename, &a.URL, &a.ContentType, &a.Size) {
		out = append(out, a)
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("failed to load attachments: %w", err)
	}
	return out, nil
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
	// Scylla refuses non-PK equality filters; we also read `deleted` and
	// skip soft-deleted rows in Go.
	query := `SELECT id, created_at, deleted FROM messages WHERE channel_id = ? AND id > ? ORDER BY id DESC`
	iter := r.session.Query(query, channelID, afterMsgID).WithContext(ctx).Iter()

	var count int
	var lastID gocql.UUID
	var lastTime time.Time
	var msgID gocql.UUID
	var createdAt time.Time
	var deleted bool

	for iter.Scan(&msgID, &createdAt, &deleted) {
		if deleted {
			continue
		}
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
	fetchLimit := limit * 2
	if fetchLimit < limit {
		fetchLimit = limit
	}
	query := `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? AND id > ? ORDER BY id ASC LIMIT ?`
	iter := r.session.Query(query, channelID, afterMsgID, fetchLimit).WithContext(ctx).Iter()

	var results []*Message
	for {
		m := &Message{}
		if !scanMessage(iter, m) {
			break
		}
		if m.Deleted {
			continue
		}
		results = append(results, m)
		if len(results) >= limit {
			break
		}
	}
	iter.Close()
	return results, nil
}

// ListRecentMessages returns up to `limit` most recent messages in a channel.
func (r *Repository) ListRecentMessages(ctx context.Context, channelID gocql.UUID, limit int) ([]*Message, error) {
	fetchLimit := limit * 2
	if fetchLimit < limit {
		fetchLimit = limit
	}
	query := `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? ORDER BY id DESC LIMIT ?`
	iter := r.session.Query(query, channelID, fetchLimit).WithContext(ctx).Iter()

	var results []*Message
	for {
		m := &Message{}
		if !scanMessage(iter, m) {
			break
		}
		if m.Deleted {
			continue
		}
		results = append(results, m)
		if len(results) >= limit {
			break
		}
	}
	iter.Close()
	return results, nil
}

// CountAllMessages counts all messages in a channel (for channels with no read_state).
func (r *Repository) CountAllMessages(ctx context.Context, channelID gocql.UUID) (int, gocql.UUID, time.Time, error) {
	query := `SELECT id, created_at, deleted FROM messages WHERE channel_id = ? ORDER BY id DESC`
	iter := r.session.Query(query, channelID).WithContext(ctx).Iter()

	var count int
	var lastID gocql.UUID
	var lastTime time.Time
	var msgID gocql.UUID
	var createdAt time.Time
	var deleted bool

	for iter.Scan(&msgID, &createdAt, &deleted) {
		if deleted {
			continue
		}
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
	cql := `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? ORDER BY id DESC LIMIT ?`
	if limit <= 0 || limit > 200 {
		limit = 200
	}
	iter := r.session.Query(cql, channelID, limit*5).WithContext(ctx).Iter() // fetch more to filter

	var results []*Message
	for {
		m := &Message{}
		if !scanMessage(iter, m) {
			break
		}
		if m.Deleted {
			continue
		}
		if strings.Contains(strings.ToLower(m.Content), strings.ToLower(query)) {
			results = append(results, m)
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
	cql := `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? ORDER BY id DESC LIMIT ?`
	iter := r.session.Query(cql, channelID, limit*10).WithContext(ctx).Iter()

	var results []*Message
	for {
		m := &Message{}
		if !scanMessage(iter, m) {
			break
		}
		if m.Deleted {
			continue
		}
		if m.ReplyToID == parentID {
			results = append(results, m)
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
	query := `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? AND updated_at > ? ALLOW FILTERING`
	iter := r.session.Query(query, channelID, since).WithContext(ctx).Iter()

	var results []*Message
	for {
		m := &Message{}
		if !scanMessage(iter, m) {
			break
		}
		results = append(results, m)
	}
	iter.Close()
	return results, nil
}
