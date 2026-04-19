package message

import (
	"context"
	"fmt"
	"time"

	"github.com/gocql/gocql"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"

	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
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

// MediaResolver resolves uploaded media file IDs into concrete attachment
// metadata. Implemented by the media service; injected here to avoid a hard
// dependency on that package.
type MediaResolver interface {
	// ResolveAttachment returns filename, presigned-URL, content_type and size
	// for a previously uploaded media file. If the file does not exist, belongs
	// to a different uploader, or is unconfirmed, an error is returned.
	ResolveAttachment(ctx context.Context, fileID, uploaderID string) (filename, url, contentType string, size int64, err error)
}

type Service struct {
	repo          *Repository
	redis         *redis.Client
	authz         *authz.Client
	nats          *realtime.Hub
	dmResolver    DMChannelResolver
	blockChecker  BlockChecker
	dmChannelList DMChannelLister
	media         MediaResolver
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

// SetMediaResolver wires the media file resolver used by attachment handling.
func (s *Service) SetMediaResolver(m MediaResolver) { s.media = m }

// dedupMentions normalises a caller-supplied list of mentioned user IDs:
// drops self-mentions, removes duplicates, ignores empty strings.
func dedupMentions(ids []string, authorID string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || id == authorID || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// GetAttachments exposes the persisted attachments for a given message so
// handlers can hydrate their proto responses.
func (s *Service) GetAttachments(ctx context.Context, channelID, messageID string) ([]Attachment, error) {
	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	msgUUID, err := gocqlParseUUID(messageID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	return s.repo.GetMessageAttachments(ctx, chUUID, msgUUID)
}

// containsEveryoneMention reports whether the message content uses
// @everyone or @here — i.e. the MENTION_EVERYONE permission should be
// enforced. Word boundaries keep "@everyone" a mention but "hi@everyone"
// inside an email-looking string not. Case-sensitive matches Discord.
func containsEveryoneMention(content string) bool {
	for _, tag := range []string{"@everyone", "@here"} {
		idx := 0
		for {
			i := indexOf(content, tag, idx)
			if i < 0 {
				break
			}
			end := i + len(tag)
			// Trailing char must not be alnum — otherwise it's a substring.
			if end == len(content) || !isAlnum(content[end]) {
				// Leading char must not be alnum either.
				if i == 0 || !isAlnum(content[i-1]) {
					return true
				}
			}
			idx = end
		}
	}
	return false
}

func indexOf(s, sub string, from int) int {
	if from >= len(s) {
		return -1
	}
	for i := from; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func isAlnum(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || b == '_'
}

// loadAttachments hydrates the persisted attachment rows for a message.
// Returns nil on any lookup error so a caller can still emit an event
// without attachments rather than failing entirely.
func (s *Service) loadAttachments(ctx context.Context, channelID, messageID gocql.UUID) []Attachment {
	atts, err := s.repo.GetMessageAttachments(ctx, channelID, messageID)
	if err != nil {
		return nil
	}
	return atts
}

// buildMessageEvent assembles a rich DM/guild-channel event payload with
// every field a client needs (reply_to_id, attachments, edited_at, deleted,
// type, pinned, mentions, reactions, forwarded_from) so subscribers don't
// have to round-trip GetMessage. Used by both DM and guild-channel publish
// paths — the subject routing happens in publishChannelEvent.
// buildMessageEvent builds a typed TextChannelEvent proto. Since the JSON
// field names match DmChatEvent too, the same payload deserialises cleanly
// into DmChatEvent on the DM-stream path — guild_id is simply ignored there.
func (s *Service) buildMessageEvent(ctx context.Context, evtType streamv1.ChatEventType, channelID, guildID, userID string, msg *Message, atts []Attachment) *streamv1.TextChannelEvent {
	evt := &streamv1.TextChannelEvent{
		Type:      evtType,
		MessageId: msg.ID.String(),
		ChannelId: channelID,
		GuildId:   guildID,
		AuthorId:  msg.AuthorID.String(),
		SenderId:  userID,
		Content:   msg.Content,
		MsgType:   int32(msg.Type),
		Pinned:    msg.Pinned,
		Deleted:   msg.Deleted,
	}

	if reactions, err := s.repo.GetReactions(ctx, msg.ChannelID, msg.ID, msg.AuthorID); err == nil && len(reactions) > 0 {
		evt.Reactions = make([]*streamv1.ChatReactionCount, len(reactions))
		for i, r := range reactions {
			evt.Reactions[i] = &streamv1.ChatReactionCount{
				Emoji: r.Emoji,
				Count: r.Count,
				Me:    r.Me,
			}
		}
	}
	var zeroUUID gocql.UUID
	if msg.ReplyToID != zeroUUID {
		evt.ReplyToId = msg.ReplyToID.String()
	}
	if msg.EditedAt != nil {
		evt.EditedAt = msg.EditedAt.Format(time.RFC3339Nano)
	}
	if !msg.CreatedAt.IsZero() {
		evt.CreatedAt = msg.CreatedAt.Format(time.RFC3339Nano)
	}
	if len(atts) > 0 {
		evt.Attachments = make([]*streamv1.ChatAttachment, len(atts))
		for i, a := range atts {
			evt.Attachments[i] = &streamv1.ChatAttachment{
				Id:          a.ID.String(),
				Filename:    a.Filename,
				Url:         a.URL,
				ContentType: a.ContentType,
				Size:        a.Size,
			}
		}
	}
	if msg.ForwardedFromMessageID != zeroUUID {
		evt.ForwardedFrom = &streamv1.ChatForwardedReference{
			ChannelId: msg.ForwardedFromChannelID.String(),
			MessageId: msg.ForwardedFromMessageID.String(),
			AuthorId:  msg.ForwardedFromAuthorID.String(),
		}
	}
	if len(msg.MentionUserIDs) > 0 {
		evt.MentionUserIds = make([]string, len(msg.MentionUserIDs))
		for i, m := range msg.MentionUserIDs {
			evt.MentionUserIds[i] = m.String()
		}
	}
	return evt
}

// publishChannelEvent fans out a message event to the right subject: a
// single guild-channel subject for guild messages, or per-member DM subjects
// so every member (including the sender's other devices) gets synced.
// publishChannelEvent accepts the typed TextChannelEvent proto. Its JSON
// field names overlap fully with DmChatEvent, so the same payload round-trips
// cleanly into a DmChatEvent on the DM stream path (guild_id is ignored).
func (s *Service) publishChannelEvent(ctx context.Context, guildID, channelID string, payload *streamv1.TextChannelEvent) {
	if s.nats == nil || payload == nil {
		return
	}
	if guildID != "" {
		_ = s.nats.Publish(realtime.GuildChannelMessage(guildID, channelID), payload)
		return
	}
	if s.dmChannelList == nil {
		return
	}
	members, _ := s.dmChannelList.GetDMChannelMemberIDs(ctx, channelID)
	for _, memberID := range members {
		_ = s.nats.Publish(realtime.DmMessage(memberID, channelID), payload)
	}
}

// ForwardSource references an existing message to be re-broadcast as the
// content of a new SendMessage call.
type ForwardSource struct {
	ChannelID string
	MessageID string
}

// SendMessage persists a new message (with optional reply, attachments,
// forward and explicit @-mentions) and fans out a "create" event on the
// channel subject. Content may be empty as long as at least one attachment
// is attached or the message is a forward.
func (s *Service) SendMessage(ctx context.Context, userID, channelID, guildID, content string, msgType int32, replyToID string, attachmentIDs []string, forward *ForwardSource, mentionIDs []string) (*Message, []Attachment, error) {
	if channelID == "" {
		return nil, nil, ErrChannelRequired
	}
	if content == "" && len(attachmentIDs) == 0 && forward == nil {
		return nil, nil, ErrContentRequired
	}

	// Guild permission cascade — check the most specific gate first so
	// the client sees a deterministic error ordering.
	if guildID != "" {
		if !s.authz.CanSendInChannel(ctx, userID, channelID) {
			return nil, nil, ErrInsufficientPermissions
		}
		if len(attachmentIDs) > 0 && !s.authz.CanAttachFiles(ctx, userID, guildID) {
			return nil, nil, ErrInsufficientPermissions
		}
		if containsEveryoneMention(content) && !s.authz.CanMentionEveryone(ctx, userID, guildID) {
			return nil, nil, ErrInsufficientPermissions
		}
	}

	chUUID, err := gocqlParseUUID(channelID)
	if err != nil {
		return nil, nil, ErrInvalidUUID
	}
	authorUUID, err := gocqlParseUUID(userID)
	if err != nil {
		return nil, nil, ErrInvalidUUID
	}

	// Resolve and dedupe mentions up-front so we can persist them on the
	// row alongside the rest of the message in a single INSERT.
	mentions := dedupMentions(mentionIDs, userID)
	mentionUUIDs := make([]gocql.UUID, 0, len(mentions))
	for _, mid := range mentions {
		mUUID, err := gocqlParseUUID(mid)
		if err != nil {
			continue
		}
		mentionUUIDs = append(mentionUUIDs, mUUID)
	}

	msg := &Message{
		ChannelID:      chUUID,
		ID:             gocql.TimeUUID(),
		AuthorID:       authorUUID,
		Content:        content,
		Type:           int(msgType),
		Pinned:         false,
		CreatedAt:      time.Now(),
		MentionUserIDs: mentionUUIDs,
	}

	// Resolve the forward source up front. We copy the source's content
	// over (if the caller didn't supply their own) and stamp the new row
	// with the source coordinates so clients can render "Forwarded from".
	var forwardedSourceAttachments []Attachment
	if forward != nil {
		srcChUUID, err := gocqlParseUUID(forward.ChannelID)
		if err != nil {
			return nil, nil, ErrInvalidUUID
		}
		srcMsgUUID, err := gocqlParseUUID(forward.MessageID)
		if err != nil {
			return nil, nil, ErrInvalidUUID
		}
		src, err := s.repo.GetMessage(ctx, srcChUUID, srcMsgUUID)
		if err != nil {
			return nil, nil, ErrForwardSourceNotFound
		}
		msg.ForwardedFromChannelID = src.ChannelID
		msg.ForwardedFromMessageID = src.ID
		msg.ForwardedFromAuthorID = src.AuthorID
		if msg.Content == "" {
			msg.Content = src.Content
		}
		// Copy the source's attachments so the forward stands on its own
		// even if the original is later deleted.
		forwardedSourceAttachments, _ = s.repo.GetMessageAttachments(ctx, srcChUUID, srcMsgUUID)
	}

	if replyToID != "" {
		replyUUID, err := gocqlParseUUID(replyToID)
		if err != nil {
			return nil, nil, ErrInvalidUUID
		}
		// Ensure the parent still exists and is not soft-deleted — replying
		// to a tombstone produces an orphan reference on the client.
		if _, err := s.repo.GetMessage(ctx, chUUID, replyUUID); err != nil {
			return nil, nil, ErrReplyParentNotFound
		}
		msg.ReplyToID = replyUUID
	}

	if err := s.repo.CreateMessage(ctx, msg); err != nil {
		return nil, nil, fmt.Errorf("failed to create message: %w", err)
	}

	// Resolve attachment metadata from the media service and persist one
	// row per attachment. If resolution of any single attachment fails, the
	// message is kept but that attachment is skipped — better than rejecting
	// the whole send after the message row was already written.
	var attachments []Attachment
	if len(attachmentIDs) > 0 && s.media != nil {
		for _, fid := range attachmentIDs {
			filename, fURL, contentType, size, err := s.media.ResolveAttachment(ctx, fid, userID)
			if err != nil {
				continue
			}
			attID, err := gocql.RandomUUID()
			if err != nil {
				continue
			}
			if err := s.repo.AddMessageAttachment(ctx, chUUID, msg.ID, attID, filename, fURL, contentType, size); err != nil {
				continue
			}
			attachments = append(attachments, Attachment{
				ID:          attID,
				Filename:    filename,
				URL:         fURL,
				ContentType: contentType,
				Size:        size,
			})
		}
	}

	// Mirror forward-source attachments under a fresh attachment id so the
	// forwarded copy is independent of the original.
	for _, a := range forwardedSourceAttachments {
		attID, err := gocql.RandomUUID()
		if err != nil {
			continue
		}
		if err := s.repo.AddMessageAttachment(ctx, chUUID, msg.ID, attID, a.Filename, a.URL, a.ContentType, a.Size); err != nil {
			continue
		}
		attachments = append(attachments, Attachment{
			ID:          attID,
			Filename:    a.Filename,
			URL:         a.URL,
			ContentType: a.ContentType,
			Size:        a.Size,
		})
	}

	// Bump mention badges for every uniquely-mentioned user (excluding
	// self). The list is supplied by the client at compose time — no
	// server-side regex parsing of content. Off-channel mentions produce
	// a harmless orphan read_state row that no one will read.
	for _, mUUID := range mentionUUIDs {
		_ = s.repo.IncrementMentionCount(ctx, mUUID, chUUID)
	}

	s.publishChannelEvent(ctx, guildID, channelID,
		s.buildMessageEvent(ctx, streamv1.ChatEventType_CHAT_EVENT_CREATE, channelID, guildID, userID, msg, attachments))

	return msg, attachments, nil
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
	} else {
		if time.Since(existing.CreatedAt) > EditWindow {
			return nil, ErrEditWindowExpired
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

	atts := s.loadAttachments(ctx, chUUID, msgUUID)
	s.publishChannelEvent(ctx, guildID, channelID,
		s.buildMessageEvent(ctx, streamv1.ChatEventType_CHAT_EVENT_UPDATE, channelID, guildID, userID, existing, atts))

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
	// Best-effort cascade — a failure here doesn't undo the soft-delete.
	_ = s.repo.CascadeDeleteChildren(ctx, chUUID, msgUUID)

	// Mirror the deleted flag on the in-memory copy we publish so clients
	// can drop the message without a follow-up fetch.
	existing.Deleted = true
	s.publishChannelEvent(ctx, guildID, channelID,
		s.buildMessageEvent(ctx, streamv1.ChatEventType_CHAT_EVENT_DELETE, channelID, guildID, userID, existing, nil))

	return nil
}

// canReadChannel unifies the "is this user allowed to read history?"
// decision across guild and DM channels. Guild channels go through
// authz.CanViewChannel (which resolves via the channel→guild binding
// and then VIEW_CHANNELS perm). DM channels don't live in OpenFGA —
// a raw Check would always deny — so we fall back to DM-member lookup.
// With neither wired (tests / degraded mode) we allow so the path
// doesn't lock users out entirely.
func (s *Service) canReadChannel(ctx context.Context, userID, channelID string) bool {
	if s.authz != nil && s.authz.CanViewChannel(ctx, userID, channelID) {
		return true
	}
	if s.dmChannelList != nil {
		members, _ := s.dmChannelList.GetDMChannelMemberIDs(ctx, channelID)
		for _, m := range members {
			if m == userID {
				return true
			}
		}
	}
	return s.authz == nil && s.dmChannelList == nil
}

func (s *Service) ListMessages(ctx context.Context, userID, channelID, before, after string, limit int32) ([]*Message, error) {
	// Access is gated by VIEW_CHANNELS for guild channels, or by DM
	// membership for DM / group-DM channels — DMs never get registered
	// in OpenFGA, so a raw CanViewChannel returns false for them.
	if !s.canReadChannel(ctx, userID, channelID) {
		return nil, ErrInsufficientPermissions
	}

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
	return s.setPinned(ctx, userID, channelID, guildID, messageID, true)
}

func (s *Service) UnpinMessage(ctx context.Context, userID, channelID, guildID, messageID string) error {
	return s.setPinned(ctx, userID, channelID, guildID, messageID, false)
}

func (s *Service) setPinned(ctx context.Context, userID, channelID, guildID, messageID string, pinned bool) error {
	// PIN_MESSAGES is a dedicated permission — different from MANAGE_CHANNEL
	// in Discord: a role that can pin need not be able to edit the channel.
	if guildID != "" && !s.authz.CanPinMessages(ctx, userID, guildID) {
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

	if err := s.repo.SetPinned(ctx, chUUID, msgUUID, pinned); err != nil {
		return err
	}

	// Load the (now pinned/unpinned) message + its attachments so the
	// broadcast carries a full snapshot that clients can render without a
	// follow-up GetMessage.
	msg, err := s.repo.GetMessage(ctx, chUUID, msgUUID)
	if err != nil {
		return nil
	}
	msg.Pinned = pinned
	atts := s.loadAttachments(ctx, chUUID, msgUUID)
	evtType := streamv1.ChatEventType_CHAT_EVENT_PIN
	if !pinned {
		evtType = streamv1.ChatEventType_CHAT_EVENT_UNPIN
	}
	s.publishChannelEvent(ctx, guildID, channelID,
		s.buildMessageEvent(ctx, evtType, channelID, guildID, userID, msg, atts))
	return nil
}

func (s *Service) AddReaction(ctx context.Context, userID, channelID, guildID, messageID, emoji string) error {
	return s.toggleReaction(ctx, userID, channelID, guildID, messageID, emoji, true)
}

func (s *Service) RemoveReaction(ctx context.Context, userID, channelID, guildID, messageID, emoji string) error {
	return s.toggleReaction(ctx, userID, channelID, guildID, messageID, emoji, false)
}

func (s *Service) toggleReaction(ctx context.Context, userID, channelID, guildID, messageID, emoji string, add bool) error {
	if emoji == "" {
		return ErrEmojiRequired
	}
	// ADD_REACTIONS is the correct gate here; Discord lets anyone *remove*
	// their own reaction regardless of perm, so only the "add" branch
	// is checked.
	if add && guildID != "" && !s.authz.CanAddReactions(ctx, userID, guildID) {
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

	// Don't allow reacting to deleted messages — matches GetMessage's
	// tombstone-hiding behavior.
	msg, err := s.repo.GetMessage(ctx, chUUID, msgUUID)
	if err != nil {
		return ErrMessageNotFound
	}

	if add {
		if err := s.repo.AddReaction(ctx, chUUID, msgUUID, emoji, uUID); err != nil {
			return err
		}
	} else {
		if err := s.repo.RemoveReaction(ctx, chUUID, msgUUID, emoji, uUID); err != nil {
			return err
		}
	}

	evtType := streamv1.ChatEventType_CHAT_EVENT_REACTION_REMOVE
	if add {
		evtType = streamv1.ChatEventType_CHAT_EVENT_REACTION_ADD
	}
	// Build the rich snapshot via buildMessageEvent so the event carries the
	// full per-emoji reactions list + attachments + reply info — receivers
	// can replace their local state wholesale instead of recomputing from
	// incremental deltas. This is what makes multi-device sync "just work":
	// every subscribed device (including the reactor's own other devices)
	// receives an authoritative state snapshot on every mutation.
	atts := s.loadAttachments(ctx, chUUID, msgUUID)
	payload := s.buildMessageEvent(ctx, evtType, channelID, guildID, userID, msg, atts)
	// Reaction-specific field. The reactor's identity already rides on
	// SenderId (set by buildMessageEvent), so no separate user_id is needed.
	payload.Emoji = emoji
	s.publishChannelEvent(ctx, guildID, channelID, payload)
	return nil
}

func (s *Service) AckMessage(ctx context.Context, userID, channelID, guildID, messageID string) error {
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

	if err := s.repo.UpsertReadState(ctx, uUID, chUUID, msgUUID); err != nil {
		return err
	}

	// Broadcast a "read_receipt" event so other members of the channel
	// (and the user's own other devices) can render "seen by X" indicators
	// in real time. Reusing TextChannelEvent keeps the publish subject and
	// JSON shape aligned with the rest of the message stream.
	payload := &streamv1.TextChannelEvent{
		Type:      streamv1.ChatEventType_CHAT_EVENT_READ_RECEIPT,
		ChannelId: channelID,
		GuildId:   guildID,
		MessageId: messageID,
		SenderId:  userID,
		AuthorId:  userID,
		EditedAt:  time.Now().Format(time.RFC3339Nano),
	}
	s.publishChannelEvent(ctx, guildID, channelID, payload)

	return nil
}

func (s *Service) StartTyping(ctx context.Context, name, userID, channelID, guildID string) error {
	if channelID == "" {
		return ErrChannelRequired
	}

	key := fmt.Sprintf("typing:%s", channelID)
	if err := s.redis.Set(ctx, key, userID, 8*time.Second).Err(); err != nil {
		return fmt.Errorf("failed to set typing indicator: %w", err)
	}
	typingEvt := &streamv1.TypingEvent{
		ChannelId: channelID,
		GuildId:   guildID,
		UserId:    userID,
		Username:  name,
		Timestamp: timestamppb.Now(),
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
	msg, _, err := s.SendMessage(ctx, senderID, channelID, "", content, 1, "", nil, nil, nil)
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
	MentionCount    int32
	LastMessageID   string
	LastMessageTime time.Time
	RecentMessages  []*Message
}

// DMChannelInfo holds metadata about a DM channel for unread display.
type DMChannelInfo struct {
	ChannelID     string
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
				MentionCount:    int32(rs.MentionCount),
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
