package message

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/redis/go-redis/v9"

	"github.com/ananddub/ndiscord_backend/internal/shared/event"
	"github.com/ananddub/ndiscord_backend/internal/shared/permissions"
)

// DMChannelResolver creates/finds DM channels. Implemented by channel service.
type DMChannelResolver interface {
	GetOrCreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (channelID string, err error)
}

// BlockChecker checks if a user is blocked. Implemented by user service.
type BlockChecker interface {
	IsBlocked(ctx context.Context, userID, targetID string) bool
}

type Service struct {
	repo        *Repository
	producer    *event.Producer
	redis       *redis.Client
	permChecker  permissions.Checker
	dmResolver   DMChannelResolver
	blockChecker BlockChecker
}

func NewService(repo *Repository, producer *event.Producer, redis *redis.Client, permChecker ...permissions.Checker) *Service {
	s := &Service{repo: repo, producer: producer, redis: redis}
	if len(permChecker) > 0 {
		s.permChecker = permChecker[0]
	}
	return s
}

// SetDMResolver sets the DM channel resolver (avoids circular deps in constructor).
func (s *Service) SetDMResolver(r DMChannelResolver) { s.dmResolver = r }

// SetBlockChecker sets the block checker.
func (s *Service) SetBlockChecker(b BlockChecker) { s.blockChecker = b }

// checkPermission returns an error if the user lacks the given permission in the guild.
// Skipped when permChecker is nil or guildID is empty (e.g. DMs).
func (s *Service) checkPermission(ctx context.Context, userID, guildID string, perm int64) error {
	if s.permChecker != nil && guildID != "" {
		if !s.permChecker.HasPermission(ctx, userID, guildID, perm) {
			return ErrInsufficientPermissions
		}
	}
	return nil
}

func (s *Service) SendMessage(ctx context.Context, userID, channelID, guildID, content string, msgType int32, replyToID string) (*Message, error) {
	if channelID == "" {
		return nil, ErrChannelRequired
	}
	if content == "" {
		return nil, ErrContentRequired
	}

	if err := s.checkPermission(ctx, userID, guildID, permissions.SendMessages); err != nil {
		return nil, err
	}

	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	authorUUID, err := gocqlParseUUID(userID)
	if err != nil {
		return nil, ErrInvalidUUID
	}

	msg := &Message{
		ChannelID: chUUID,
		ID:        gocql.TimeUUID(),
		AuthorID:  authorUUID,
		Content:   content,
		Type:      int(msgType),
		Pinned:    false,
		CreatedAt: time.Now(),
	}

	if replyToID != "" {
		replyUUID, err := gocqlParseUUID(replyToID)
		if err != nil {
			return nil, ErrInvalidUUID
		}
		msg.ReplyToID = replyUUID
	}

	if err := s.repo.CreateMessage(ctx, msg); err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	_ = event.PublishEvent(ctx, s.producer, event.TopicMessageCreate, channelID, guildID, channelID, userID, map[string]any{
		"id":         msg.ID.String(),
		"channel_id": channelID,
		"guild_id":   guildID,
		"author_id":  userID,
		"content":    content,
	})

	return msg, nil
}

func (s *Service) GetMessage(ctx context.Context, channelID, messageID string) (*Message, error) {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return nil, ErrInvalidUUID
	}

	msg, err := s.repo.GetMessage(ctx, chUUID, msgUUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMessageNotFound, err)
	}
	return msg, nil
}

func (s *Service) EditMessage(ctx context.Context, userID, channelID, guildID, messageID, content string) (*Message, error) {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	authorUUID, err := gocqlParseUUID(userID)
	if err != nil {
		return nil, ErrInvalidUUID
	}

	// Verify author or ManageMessages permission
	existing, err := s.repo.GetMessage(ctx, chUUID, msgUUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMessageNotFound, err)
	}
	if existing.AuthorID != authorUUID {
		if err := s.checkPermission(ctx, userID, guildID, permissions.ManageMessages); err != nil {
			return nil, ErrNotMessageAuthor
		}
	}

	now := time.Now()
	if err := s.repo.UpdateMessageContent(ctx, chUUID, msgUUID, content, now); err != nil {
		return nil, fmt.Errorf("failed to edit message: %w", err)
	}

	existing.Content = content
	existing.EditedAt = &now

	_ = event.PublishEvent(ctx, s.producer, event.TopicMessageUpdate, channelID, "", channelID, userID, map[string]any{
		"id":         messageID,
		"channel_id": channelID,
		"content":    content,
	})

	return existing, nil
}

func (s *Service) DeleteMessage(ctx context.Context, userID, channelID, guildID, messageID string) error {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return ErrInvalidUUID
	}
	authorUUID, err := gocqlParseUUID(userID)
	if err != nil {
		return ErrInvalidUUID
	}

	existing, err := s.repo.GetMessage(ctx, chUUID, msgUUID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMessageNotFound, err)
	}
	if existing.AuthorID != authorUUID {
		if err := s.checkPermission(ctx, userID, guildID, permissions.ManageMessages); err != nil {
			return ErrNotMessageAuthor
		}
	}

	if err := s.repo.DeleteMessage(ctx, chUUID, msgUUID); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	_ = event.PublishEvent(ctx, s.producer, event.TopicMessageDelete, channelID, "", channelID, userID, map[string]any{
		"id":         messageID,
		"channel_id": channelID,
	})

	return nil
}

func (s *Service) ListMessages(ctx context.Context, userID, channelID, before, after string, limit int32) ([]*Message, error) {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}

	l := int(limit)
	if l <= 0 || l > 100 {
		l = 50
	}

	var beforePtr, afterPtr *gocql.UUID
	if before != "" {
		u, err := gocqlParseUUID(before)
		if err != nil {
			return nil, ErrInvalidUUID
		}
		beforePtr = &u
	}
	if after != "" {
		u, err := gocqlParseUUID(after)
		if err != nil {
			return nil, ErrInvalidUUID
		}
		afterPtr = &u
	}

	return s.repo.ListMessages(ctx, chUUID, beforePtr, afterPtr, l)
}

func (s *Service) PinMessage(ctx context.Context, userID, channelID, guildID, messageID string) error {
	if err := s.checkPermission(ctx, userID, guildID, permissions.ManageMessages); err != nil {
		return err
	}

	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return ErrInvalidUUID
	}

	return s.repo.SetPinned(ctx, chUUID, msgUUID, true)
}

func (s *Service) UnpinMessage(ctx context.Context, userID, channelID, guildID, messageID string) error {
	if err := s.checkPermission(ctx, userID, guildID, permissions.ManageMessages); err != nil {
		return err
	}

	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return ErrInvalidUUID
	}

	return s.repo.SetPinned(ctx, chUUID, msgUUID, false)
}

func (s *Service) AddReaction(ctx context.Context, userID, channelID, guildID, messageID, emoji string) error {
	if emoji == "" {
		return ErrEmojiRequired
	}

	if err := s.checkPermission(ctx, userID, guildID, permissions.AddReactions); err != nil {
		return err
	}

	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return ErrInvalidUUID
	}
	uUID, err := gocqlParseUUID(userID)
	if err != nil {
		return ErrInvalidUUID
	}

	return s.repo.AddReaction(ctx, chUUID, msgUUID, emoji, uUID)
}

func (s *Service) RemoveReaction(ctx context.Context, userID, channelID, messageID, emoji string) error {
	if emoji == "" {
		return ErrEmojiRequired
	}
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return ErrInvalidUUID
	}
	uUID, err := gocqlParseUUID(userID)
	if err != nil {
		return ErrInvalidUUID
	}

	return s.repo.RemoveReaction(ctx, chUUID, msgUUID, emoji, uUID)
}

func (s *Service) AckMessage(ctx context.Context, userID, channelID, messageID string) error {
	uUID, err := gocqlParseUUID(userID)
	if err != nil {
		return ErrInvalidUUID
	}
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return ErrInvalidUUID
	}

	return s.repo.UpsertReadState(ctx, uUID, chUUID, msgUUID)
}

func (s *Service) StartTyping(ctx context.Context, userID, channelID string) error {
	if channelID == "" {
		return ErrChannelRequired
	}

	// Store typing indicator in Redis with 8s TTL
	key := fmt.Sprintf("typing:%s", channelID)
	if err := s.redis.Set(ctx, key, userID, 8*time.Second).Err(); err != nil {
		return fmt.Errorf("failed to set typing indicator: %w", err)
	}

	// Publish typing event
	_ = event.PublishEvent(ctx, s.producer, event.TopicTyping, channelID, "", channelID, userID, map[string]any{
		"channel_id": channelID,
		"user_id":    userID,
	})

	return nil
}

// GetReactions retrieves aggregated reaction counts for a message.
func (s *Service) GetReactions(ctx context.Context, channelID, messageID, currentUserID string) ([]ReactionCount, error) {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	curUUID, err := gocqlParseUUID(currentUserID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	return s.repo.GetReactions(ctx, chUUID, msgUUID, curUUID)
}

// SendDirectMessage sends a message directly to a user. Auto-creates DM channel if needed.
func (s *Service) SendDirectMessage(ctx context.Context, senderID, recipientID, content string) (string, *Message, error) {
	if recipientID == "" {
		return "", nil, fmt.Errorf("recipient_id is required")
	}
	if content == "" {
		return "", nil, ErrContentRequired
	}
	if s.dmResolver == nil {
		return "", nil, fmt.Errorf("DM not configured")
	}

	// Block check: either direction
	if s.blockChecker != nil {
		if s.blockChecker.IsBlocked(ctx, senderID, recipientID) || s.blockChecker.IsBlocked(ctx, recipientID, senderID) {
			return "", nil, ErrUserBlocked
		}
	}

	// Get or create DM channel
	channelID, err := s.dmResolver.GetOrCreateDMChannel(ctx, senderID, []string{recipientID})
	if err != nil {
		return "", nil, fmt.Errorf("failed to get/create DM channel: %w", err)
	}

	// Send message in that channel
	msg, err := s.SendMessage(ctx, senderID, channelID, "", content, 1, "")
	if err != nil {
		return "", nil, err
	}

	return channelID, msg, nil
}

// UnreadInfo holds unread count for a channel.
type UnreadInfo struct {
	ChannelID       string
	LastReadMsgID   string
	UnreadCount     int32
	LastMessageID   string
	LastMessageTime time.Time
}

// GetUnreadCounts returns unread message counts for all channels the user has read states in.
func (s *Service) GetUnreadCounts(ctx context.Context, userID string) ([]UnreadInfo, int32, error) {
	uUID, err := gocqlParseUUID(userID)
	if err != nil {
		return nil, 0, ErrInvalidUUID
	}

	// Get all read states for user
	readStates, err := s.repo.GetUserReadStates(ctx, uUID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get read states: %w", err)
	}

	var results []UnreadInfo
	var totalUnread int32

	for _, rs := range readStates {
		// Count messages after last_read_message_id
		count, lastMsg, lastTime, err := s.repo.CountUnreadMessages(ctx, rs.ChannelID, rs.LastReadMessageID)
		if err != nil {
			continue
		}

		if count > 0 {
			info := UnreadInfo{
				ChannelID:       rs.ChannelID.String(),
				LastReadMsgID:   rs.LastReadMessageID.String(),
				UnreadCount:     int32(count),
				LastMessageID:   lastMsg.String(),
				LastMessageTime: lastTime,
			}
			results = append(results, info)
			totalUnread += int32(count)
		}
	}

	return results, totalUnread, nil
}

// gocqlParseUUID parses a string into a gocql.UUID.
func gocqlParseUUID(s string) (gocql.UUID, error) {
	u, err := gocql.ParseUUID(s)
	if err != nil {
		return gocql.UUID{}, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	return u, nil
}
