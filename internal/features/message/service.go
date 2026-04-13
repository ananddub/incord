package message

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/redis/go-redis/v9"

	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

// DMChannelResolver creates/finds DM channels. Implemented by channel service.
type DMChannelResolver interface {
	GetOrCreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (channelID string, err error)
}

// BlockChecker checks if a user is blocked. Implemented by user service.
type BlockChecker interface {
	IsBlocked(ctx context.Context, userID, targetID string) bool
}

// DMChannelLister lists all DM channels a user is part of with metadata.
type DMChannelLister interface {
	GetUserDMChannelIDs(ctx context.Context, userID string) ([]string, error)
	GetDMChannelMemberIDs(ctx context.Context, channelID string) ([]string, error)
}

type Service struct {
	repo          *Repository
	redis         *redis.Client
	authz         *authz.Client
	nats          *realtime.Hub
	dmResolver    DMChannelResolver
	blockChecker  BlockChecker
	dmChannelList DMChannelLister
}

func NewService(repo *Repository, redis *redis.Client, nats *realtime.Hub, authzClient ...*authz.Client) *Service {
	s := &Service{repo: repo, redis: redis, nats: nats}
	if len(authzClient) > 0 {
		s.authz = authzClient[0]
	}
	return s
}

// SetDMResolver sets the DM channel resolver (avoids circular deps in constructor).
func (s *Service) SetDMResolver(r DMChannelResolver) { s.dmResolver = r }

// SetBlockChecker sets the block checker.
func (s *Service) SetBlockChecker(b BlockChecker) { s.blockChecker = b }

// SetDMChannelLister sets the DM channel lister for unread counts.
func (s *Service) SetDMChannelLister(l DMChannelLister) { s.dmChannelList = l }

func (s *Service) SendMessage(ctx context.Context, userID, channelID, guildID, content string, msgType int32, replyToID string) (*Message, error) {
	if channelID == "" {
		return nil, ErrChannelRequired
	}
	if content == "" {
		return nil, ErrContentRequired
	}

	// For guild channels, check send permission via authz
	if guildID != "" && !s.authz.CanSendInChannel(ctx, userID, channelID) {
		return nil, ErrInsufficientPermissions
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

	// Publish to the correct subject based on guild vs DM
	createEvt := map[string]any{
		"type":       "create",
		"message_id": msg.ID.String(),
		"channel_id": channelID,
		"guild_id":   guildID,
		"author_id":  userID,
		"sender_id":  userID,
		"content":    content,
	}
	if guildID != "" {
		_ = s.nats.Publish(realtime.GuildChannelMessage(guildID, channelID), createEvt)
	} else if s.dmChannelList != nil {
		members, _ := s.dmChannelList.GetDMChannelMemberIDs(ctx, channelID)
		for _, memberID := range members {
			if memberID != userID {
				_ = s.nats.Publish(realtime.DmMessage(memberID, channelID), createEvt)
			}
		}
	}

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

	// Verify author or ManageChannel permission
	existing, err := s.repo.GetMessage(ctx, chUUID, msgUUID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMessageNotFound, err)
	}
	if existing.AuthorID != authorUUID {
		if guildID == "" || !s.authz.CanManageChannel(ctx, userID, channelID) {
			return nil, ErrNotMessageAuthor
		}
	}

	now := time.Now()
	if err := s.repo.UpdateMessageContent(ctx, chUUID, msgUUID, content, now); err != nil {
		return nil, fmt.Errorf("failed to edit message: %w", err)
	}

	// Save edit history before overwriting
	_ = s.repo.SaveEditHistory(ctx, chUUID, msgUUID, existing.Content, now)

	existing.Content = content
	existing.EditedAt = &now

	updateEvt := map[string]any{
		"type":       "update",
		"message_id": messageID,
		"channel_id": channelID,
		"sender_id":  userID,
		"author_id":  userID,
		"content":    content,
	}
	if guildID != "" {
		_ = s.nats.Publish(realtime.GuildChannelMessage(guildID, channelID), updateEvt)
	} else if s.dmChannelList != nil {
		members, _ := s.dmChannelList.GetDMChannelMemberIDs(ctx, channelID)
		for _, memberID := range members {
			if memberID != userID {
				_ = s.nats.Publish(realtime.DmMessage(memberID, channelID), updateEvt)
			}
		}
	}

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
		if guildID == "" || !s.authz.CanManageChannel(ctx, userID, channelID) {
			return ErrNotMessageAuthor
		}
	}

	if err := s.repo.DeleteMessage(ctx, chUUID, msgUUID); err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	deleteEvt := map[string]any{
		"type":       "delete",
		"message_id": messageID,
		"channel_id": channelID,
		"sender_id":  userID,
	}
	if guildID != "" {
		_ = s.nats.Publish(realtime.GuildChannelMessage(guildID, channelID), deleteEvt)
	} else if s.dmChannelList != nil {
		members, _ := s.dmChannelList.GetDMChannelMemberIDs(ctx, channelID)
		for _, memberID := range members {
			if memberID != userID {
				_ = s.nats.Publish(realtime.DmMessage(memberID, channelID), deleteEvt)
			}
		}
	}

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
	if guildID != "" && !s.authz.CanManageChannel(ctx, userID, channelID) {
		return ErrInsufficientPermissions
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
	if guildID != "" && !s.authz.CanManageChannel(ctx, userID, channelID) {
		return ErrInsufficientPermissions
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

	// For guild channels, check view permission as proxy for reaction permission
	if guildID != "" && !s.authz.CanViewChannel(ctx, userID, channelID) {
		return ErrInsufficientPermissions
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

func (s *Service) StartTyping(ctx context.Context, userID, channelID, guildID string) error {
	if channelID == "" {
		return ErrChannelRequired
	}

	// Store typing indicator in Redis with 8s TTL
	key := fmt.Sprintf("typing:%s", channelID)
	if err := s.redis.Set(ctx, key, userID, 8*time.Second).Err(); err != nil {
		return fmt.Errorf("failed to set typing indicator: %w", err)
	}

	// Publish typing event
	typingEvt := map[string]any{
		"event":      "TYPING_START",
		"channel_id": channelID,
		"user_id":    userID,
	}
	if guildID != "" {
		_ = s.nats.Publish(realtime.GuildChannelTyping(guildID, channelID), typingEvt)
	} else if s.dmChannelList != nil {
		members, _ := s.dmChannelList.GetDMChannelMemberIDs(ctx, channelID)
		for _, memberID := range members {
			if memberID != userID {
				_ = s.nats.Publish(realtime.DmTyping(memberID, channelID), typingEvt)
			}
		}
	}

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
	GuildID         string // empty for DMs
	ChannelName     string
	IsDM            bool
	SenderID        string // for DM: the other person
	SenderName      string
	LastReadMsgID   string
	UnreadCount     int32
	LastMessageID   string
	LastMessageTime time.Time
	RecentMessages  []*Message
}

// DMChannelInfo holds metadata about a DM channel for unread display.
type DMChannelInfo struct {
	ChannelID  string
	OtherUserID   string
	OtherUserName string
}

// GetUnreadCounts returns unread message counts for ALL channels the user is part of
// (DM channels + guild channels with read states). If user never opened a DM, all
// messages in that DM count as unread.
func (s *Service) GetUnreadCounts(ctx context.Context, userID string) ([]UnreadInfo, int32, error) {
	uUID, err := gocqlParseUUID(userID)
	if err != nil {
		return nil, 0, ErrInvalidUUID
	}

	// Track which channels we've already counted
	seen := make(map[string]bool)
	var results []UnreadInfo
	var totalUnread int32

	// 1. Channels with read_states (user has read before)
	readStates, err := s.repo.GetUserReadStates(ctx, uUID)
	if err == nil {
		for _, rs := range readStates {
			chID := rs.ChannelID.String()
			seen[chID] = true

			count, lastMsg, lastTime, err := s.repo.CountUnreadMessages(ctx, rs.ChannelID, rs.LastReadMessageID)
			if err != nil || count == 0 {
				continue
			}
			// Fetch up to 5 recent unread messages for preview
			recent, _ := s.repo.ListMessagesAfter(ctx, rs.ChannelID, rs.LastReadMessageID, 5)
			results = append(results, UnreadInfo{
				ChannelID:       chID,
				LastReadMsgID:   rs.LastReadMessageID.String(),
				UnreadCount:     int32(count),
				LastMessageID:   lastMsg.String(),
				LastMessageTime: lastTime,
				RecentMessages:  recent,
			})
			totalUnread += int32(count)
		}
	}

	// 2. DM channels user is part of (mark as DM, resolve other user)
	if s.dmChannelList != nil {
		dmChannelIDs, err := s.dmChannelList.GetUserDMChannelIDs(ctx, userID)
		if err == nil {
			// Mark already-seen channels as DM too
			dmSet := make(map[string]bool)
			for _, id := range dmChannelIDs {
				dmSet[id] = true
			}
			for i, r := range results {
				if dmSet[r.ChannelID] {
					results[i].IsDM = true
					results[i].SenderID = s.resolveDMOtherUser(ctx, r.ChannelID, userID)
				}
			}

			// Add never-opened DM channels
			for _, chID := range dmChannelIDs {
				if seen[chID] {
					continue
				}

				chUUID, err := gocqlParseUUID(chID)
				if err != nil {
					continue
				}

				count, lastMsg, lastTime, err := s.repo.CountAllMessages(ctx, chUUID)
				if err != nil || count == 0 {
					continue
				}

				recent, _ := s.repo.ListRecentMessages(ctx, chUUID, 5)
				results = append(results, UnreadInfo{
					ChannelID:       chID,
					IsDM:            true,
					SenderID:        s.resolveDMOtherUser(ctx, chID, userID),
					UnreadCount:     int32(count),
					LastMessageID:   lastMsg.String(),
					LastMessageTime: lastTime,
					RecentMessages:  recent,
				})
				totalUnread += int32(count)
			}
		}
	}

	return results, totalUnread, nil
}

// resolveDMOtherUser finds the other person in a DM channel.
func (s *Service) resolveDMOtherUser(ctx context.Context, channelID, userID string) string {
	if s.dmChannelList == nil {
		return ""
	}
	members, err := s.dmChannelList.GetDMChannelMemberIDs(ctx, channelID)
	if err != nil {
		return ""
	}
	for _, m := range members {
		if m != userID {
			return m
		}
	}
	return ""
}

func (s *Service) SearchMessages(ctx context.Context, channelID, query string, limit int32) ([]*Message, error) {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	return s.repo.SearchMessages(ctx, chUUID, query, int(limit))
}

func (s *Service) GetThreadMessages(ctx context.Context, channelID, parentMsgID string, limit int32) ([]*Message, error) {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	parentUUID, err := gocqlParseUUID(parentMsgID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	return s.repo.GetThreadMessages(ctx, chUUID, parentUUID, int(limit))
}

func (s *Service) BulkDeleteMessages(ctx context.Context, userID, channelID, guildID string, messageIDs []string) (int, error) {
	if guildID != "" && s.authz != nil && !s.authz.CanManageChannel(ctx, userID, channelID) {
		return 0, ErrInsufficientPermissions
	}
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return 0, ErrInvalidUUID
	}
	var uuids []gocql.UUID
	for _, id := range messageIDs {
		u, err := gocqlParseUUID(id)
		if err != nil {
			continue
		}
		uuids = append(uuids, u)
	}
	return s.repo.BulkDeleteMessages(ctx, chUUID, uuids)
}

func (s *Service) GetEditHistory(ctx context.Context, channelID, messageID string) ([]EditHistory, error) {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	return s.repo.GetEditHistory(ctx, chUUID, msgUUID)
}

// gocqlParseUUID parses a string into a gocql.UUID.
func gocqlParseUUID(s string) (gocql.UUID, error) {
	u, err := gocql.ParseUUID(s)
	if err != nil {
		return gocql.UUID{}, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	return u, nil
}
