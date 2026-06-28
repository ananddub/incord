package message

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ananddub/ndiscord_backend/gen/scylladb"
	"github.com/gocql/gocql"
)

type Repository struct {
	session *gocql.Session
	queries *scylladb.Queries
}

func NewRepository(session *gocql.Session) *Repository {
	flq := scylladb.Newq(session)
	return &Repository{session: session, queries: flq}
}

func (r *Repository) CreateMessage(ctx context.Context, msg *scylladb.CreatemessageParams) error {
	return r.queries.Createmessage(ctx, *msg)
}

func (r *Repository) GetMessage(ctx context.Context, channelID, messageID gocql.UUID) (*scylladb.Messages, error) {
	msg, err := r.queries.Getmessage(ctx, channelID, messageID)
	if err != nil {
		return nil, err
	}
	if boolValue(msg.Deleted) {
		return nil, gocql.ErrNotFound
	}

	return &msg, nil
}

func (r *Repository) UpdateMessageContent(ctx context.Context, channelID, messageID gocql.UUID, content string, editedAt time.Time) error {
	return r.queries.Updatemessagecontent(ctx, scylladb.UpdatemessagecontentParams{
		Content:   content,
		EditedAt:  editedAt,
		ChannelId: channelID,
		Id:        messageID,
	})
}

func (r *Repository) DeleteMessage(ctx context.Context, channelID, messageID gocql.UUID) error {
	return r.queries.Deletemessage(ctx, time.Now(), channelID, messageID)
}

func (r *Repository) IncrementMentionCount(ctx context.Context, userID, channelID gocql.UUID) error {
	value, err := r.queries.Getreadstate(ctx, userID, channelID)
	if err != nil && err != gocql.ErrNotFound {
		return err
	}
	value.MentionCount++
	return r.queries.Upsertreadstate(ctx, scylladb.UpsertreadstateParams{
		UserId:            userID,
		ChannelId:         channelID,
		LastReadMessageId: value.LastReadMessageId,
		MentionCount:      value.MentionCount,
	})
}

func (r *Repository) CascadeDeleteChildren(ctx context.Context, channelID, messageID gocql.UUID) error {
	r.queries.Deletemessageattachments(ctx, channelID, messageID)
	r.queries.Deletemessagereactions(ctx, channelID, messageID)
	r.queries.Deleteedithistory(ctx, channelID, messageID)
	return nil
}

func (r *Repository) ListMessages(ctx context.Context, channelID gocql.UUID, before, after *gocql.UUID, limit int) ([]*scylladb.Messages, error) {
	fetchLimit := limit * 2
	if fetchLimit < limit {
		fetchLimit = limit
	}
	var smsg []scylladb.Messages
	switch {
	case before != nil:
		msg, err := r.queries.Listmessagesbefore(ctx, channelID, *before, int64(fetchLimit))
		if err != nil {
			return nil, fmt.Errorf("failed to list messages: %w", err)
		}
		smsg = msg
	case after != nil:
		msg, err := r.queries.Listmessagesafter(ctx, channelID, *after, int64(fetchLimit))
		if err != nil {
			return nil, fmt.Errorf("failed to list messages: %w", err)
		}
		smsg = msg
	default:
		msg, err := r.queries.Listrecentmessages(ctx, channelID, int64(fetchLimit))
		if err != nil {
			return nil, fmt.Errorf("failed to list messages: %w", err)
		}
		smsg = msg
	}
	results := make([]*scylladb.Messages, 0, len(smsg))
	for i := range smsg {
		if smsg[i].Deleted != nil && *smsg[i].Deleted {
			continue
		}
		results = append(results, &smsg[i])
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func (r *Repository) SetPinned(ctx context.Context, channelID, messageID gocql.UUID, pinned bool) error {
	return r.queries.Setpinned(ctx, pinned, channelID, messageID)
}

func (r *Repository) AddMessageAttachment(ctx context.Context, channelID, messageID, attachmentID gocql.UUID, filename, urlStr, contentType string, size int64) error {
	return r.queries.Addmessageattachment(ctx, scylladb.AddmessageattachmentParams{
		ChannelId:   channelID,
		MessageId:   messageID,
		Id:          attachmentID,
		Filename:    filename,
		Url:         urlStr,
		ContentType: contentType,
		Size:        size,
	})
}

func (r *Repository) GetMessageAttachments(ctx context.Context, channelID, messageID gocql.UUID) ([]scylladb.MessageAttachments, error) {
	return r.queries.Getmessageattachments(ctx, channelID, messageID)
}

func (r *Repository) AddReaction(ctx context.Context, channelID, messageID gocql.UUID, emoji string, userID gocql.UUID) error {
	return r.queries.Addreaction(ctx, scylladb.AddreactionParams{
		ChannelId: channelID,
		MessageId: messageID,
		Emoji:     emoji,
		UserId:    userID,
	})
}

func (r *Repository) RemoveReaction(ctx context.Context, channelID, messageID gocql.UUID, emoji string, userID gocql.UUID) error {
	return r.queries.Removereaction(ctx, scylladb.RemovereactionParams{
		ChannelId: channelID,
		UserId:    userID,
		Emoji:     emoji,
		MessageId: messageID,
	})
}

func (r *Repository) GetReactions(ctx context.Context, channelID, messageID gocql.UUID, currentUserID gocql.UUID) ([]ReactionCount, error) {
	data, err := r.queries.Getreactions(ctx, channelID, messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get reactions: %w", err)
	}
	emojiUsers := make(map[string][]gocql.UUID)
	for _, r := range data {
		emojiUsers[r.Emoji] = append(emojiUsers[r.Emoji], r.UserId)
	}
	var result []ReactionCount
	for e, users := range emojiUsers {
		me := false

		users_str := make([]string, len(users))
		for _, u := range users {
			users_str = append(users_str, u.String())
			if u == currentUserID {
				me = true
				break
			}
		}
		result = append(result, ReactionCount{
			user_id: users_str,
			Emoji:   e,
			Count:   int32(len(users)),
			Me:      me,
		})
	}
	return result, nil
}

func (r *Repository) UpsertReadState(ctx context.Context, userID, channelID, messageID gocql.UUID) error {
	return r.queries.Upsertreadstate(ctx, scylladb.UpsertreadstateParams{
		UserId:            userID,
		ChannelId:         channelID,
		LastReadMessageId: messageID,
		MentionCount:      0,
	})
}

func (r *Repository) GetUserReadStates(ctx context.Context, userID gocql.UUID) ([]scylladb.ReadStates, error) {
	return r.queries.Getuserreadstates(ctx, userID)
}

func (r *Repository) CountUnreadMessages(ctx context.Context, channelID, afterMsgID gocql.UUID) (int, gocql.UUID, time.Time, error) {
	data, err := r.queries.Countunreadmessages(ctx, channelID, afterMsgID)
	if err != nil {
		return 0, gocql.UUID{}, time.Time{}, fmt.Errorf("failed to count unread messages: %w", err)
	}
	var count int
	var lastID *gocql.UUID
	var lastTime *time.Time

	for _, msg := range data {
		if boolValue(msg.Deleted) {
			continue
		}
		count++
		if count == 1 {
			lastID = msg.Id
			lastTime = msg.CreatedAt
		}
	}
	if count == 0 {
		return 0, gocql.UUID{}, time.Time{}, nil
	}
	return count, uuidValue(lastID), timeValue(lastTime), nil
}

// ListMessagesAfter returns up to `limit` messages after the given message ID (for unread previews).
func (r *Repository) ListMessagesAfter(ctx context.Context, channelID, afterMsgID gocql.UUID, limit int) ([]*scylladb.Messages, error) {
	fetchLimit := limit * 2
	if fetchLimit < limit {
		fetchLimit = limit
	}
	data, err := r.queries.Listmessagesafter(ctx, channelID, afterMsgID, int64(fetchLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to list messages after: %w", err)
	}
	var results []*scylladb.Messages
	for _, m := range data {
		if boolValue(m.Deleted) {
			continue
		}
		results = append(results, &m)
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

func (r *Repository) ListRecentMessages(ctx context.Context, channelID gocql.UUID, limit int) ([]*scylladb.Messages, error) {
	fetchLimit := limit * 2
	if fetchLimit < limit {
		fetchLimit = limit
	}
	data, err := r.queries.Listrecentmessages(ctx, channelID, int64(fetchLimit))
	if err != nil {
		return nil, fmt.Errorf("failed to list recent messages: %w", err)
	}
	var results []*scylladb.Messages
	for _, m := range data {
		if boolValue(m.Deleted) {
			continue
		}
		results = append(results, &m)
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

// CountAllMessages counts all messages in a channel (for channels with no read_state).
func (r *Repository) CountAllMessages(ctx context.Context, channelID gocql.UUID) (int, gocql.UUID, time.Time, error) {
	data, err := r.queries.Countallmessages(ctx, channelID)
	if err != nil {
		return 0, gocql.UUID{}, time.Time{}, fmt.Errorf("failed to count all messages: %w", err)
	}
	var count int
	var lastID gocql.UUID
	var lastTime time.Time

	for _, m := range data {
		if boolValue(m.Deleted) {
			continue
		}
		count++
		if count == 1 {
			lastID = uuidValue(m.Id)
			lastTime = timeValue(m.CreatedAt)
		}
	}
	return count, lastID, lastTime, nil
}

func (r *Repository) SaveEditHistory(ctx context.Context, channelID, messageID gocql.UUID, oldContent string, editedAt time.Time) error {

	return r.queries.Saveedithistory(ctx, scylladb.SaveedithistoryParams{
		ChannelId:  channelID,
		MessageId:  messageID,
		OldContent: oldContent,
		EditedAt:   editedAt,
	})
}

func (r *Repository) GetEditHistory(ctx context.Context, channelID, messageID gocql.UUID) ([]scylladb.MessageEditHistory, error) {
	return r.queries.Getedithistory(ctx, channelID, messageID)
}

func (r *Repository) SearchMessages(ctx context.Context, channelID gocql.UUID, query string, limit int) ([]*scylladb.Messages, error) {
	data, err := r.queries.Listrecentmessages(ctx, channelID, int64(limit*5))
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}
	var results []*scylladb.Messages
	for _, m := range data {
		if boolValue(m.Deleted) {
			continue
		}
		if strings.Contains(strings.ToLower(stringValue(m.Content)), strings.ToLower(query)) {
			results = append(results, &m)
			if len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

func (r *Repository) GetThreadMessages(ctx context.Context, channelID, parentID gocql.UUID, limit int) ([]*scylladb.Messages, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	data, err := r.queries.Listrecentmessages(ctx, channelID, int64(limit*10))
	if err != nil {
		return nil, fmt.Errorf("failed to list recent messages for thread: %w", err)
	}
	var results []*scylladb.Messages
	for _, m := range data {
		if boolValue(m.Deleted) {
			continue
		}
		if uuidValue(m.ReplyToId) == parentID {
			results = append(results, &m)
			if len(results) >= limit {
				break
			}
		}
	}
	return results, nil
}

func (r *Repository) BulkDeleteMessages(ctx context.Context, channelID gocql.UUID, messageIDs []gocql.UUID) (int, error) {
	deleted := 0
	for _, msgID := range messageIDs {
		if err := r.DeleteMessage(ctx, channelID, msgID); err == nil {
			deleted++
		}
	}
	return deleted, nil
}

func (r *Repository) GetMessagesSince(ctx context.Context, channelID gocql.UUID, since time.Time) ([]scylladb.Messages, error) {
	// query := `SELECT ` + messageColumns + ` FROM messages WHERE channel_id = ? AND updated_at > ? ALLOW FILTERING`
	return r.queries.Getmessagessince(ctx, channelID, since)

}
