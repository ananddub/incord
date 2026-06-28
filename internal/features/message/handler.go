package message

import (
	"context"
	"errors"

	"github.com/gocql/gocql"

	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type ChannelGuildResolver interface {
	GetChannelGuildID(ctx context.Context, channelID string) string
}

type Handler struct {
	messagev1.UnimplementedMessageServiceServer
	svc           *Service
	guildResolver ChannelGuildResolver
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) SetGuildResolver(r ChannelGuildResolver) { h.guildResolver = r }

func (h *Handler) resolveGuildID(ctx context.Context, channelID string) string {
	if h.guildResolver != nil {
		return h.guildResolver.GetChannelGuildID(ctx, channelID)
	}
	return ""
}

func (h *Handler) SendMessage(ctx context.Context, req *messagev1.SendMessageRequest) (*messagev1.SendMessageResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if req.Content == "" && len(req.AttachmentIds) == 0 {
		return nil, status.Error(codes.InvalidArgument, "content or attachments required")
	}

	guildID := h.resolveGuildID(ctx, req.ChannelId)

	var fwd *ForwardSource
	if req.ForwardedFrom != nil && req.ForwardedFrom.GetMessageId() != "" {
		fwd = &ForwardSource{
			ChannelID: req.ForwardedFrom.GetChannelId(),
			MessageID: req.ForwardedFrom.GetMessageId(),
		}
	}

	msg, atts, err := h.svc.SendMessage(ctx, userID, req.ChannelId, guildID, req.Content, int32(req.Type), req.ReplyToId, req.AttachmentIds, fwd, req.MentionUserIds)
	if err != nil {
		return nil, mapError(err)
	}

	pbMsg, err := h.messageToProto(ctx, msg, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to build response")
	}
	pbMsg.Attachments = attachmentsToProto(atts)

	return &messagev1.SendMessageResponse{
		Message: pbMsg,
	}, nil
}

func (h *Handler) GetMessage(ctx context.Context, req *messagev1.GetMessageRequest) (*messagev1.GetMessageResponse, error) {
	userID := middleware.UserIDFromContext(ctx)

	msg, err := h.svc.GetMessage(ctx, req.ChannelId, req.MessageId)
	if err != nil {
		return nil, mapError(err)
	}

	pbMsg, err := h.messageToProto(ctx, msg, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to build response")
	}

	return &messagev1.GetMessageResponse{
		Message: pbMsg,
	}, nil
}

func (h *Handler) EditMessage(ctx context.Context, req *messagev1.EditMessageRequest) (*messagev1.EditMessageResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	msg, err := h.svc.EditMessage(ctx, userID, req.ChannelId, h.resolveGuildID(ctx, req.ChannelId), req.MessageId, req.Content)
	if err != nil {
		return nil, mapError(err)
	}

	pbMsg, err := h.messageToProto(ctx, msg, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to build response")
	}

	return &messagev1.EditMessageResponse{
		Message: pbMsg,
	}, nil
}

func (h *Handler) DeleteMessage(ctx context.Context, req *messagev1.DeleteMessageRequest) (*messagev1.DeleteMessageResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.svc.DeleteMessage(ctx, userID, req.ChannelId, h.resolveGuildID(ctx, req.ChannelId), req.MessageId); err != nil {
		return nil, mapError(err)
	}

	return &messagev1.DeleteMessageResponse{}, nil
}

func (h *Handler) ListMessages(ctx context.Context, req *messagev1.ListMessagesRequest) (*messagev1.ListMessagesResponse, error) {
	userID := middleware.UserIDFromContext(ctx)

	messages, err := h.svc.ListMessages(ctx, userID, req.ChannelId, req.Before, req.After, req.Limit)
	if err != nil {
		return nil, mapError(err)
	}

	pbMessages := make([]*messagev1.Message, len(messages))
	for i, msg := range messages {
		pb, err := h.messageToProto(ctx, msg, userID)
		if err != nil {
			return nil, status.Error(codes.Internal, "failed to build response")
		}
		pbMessages[i] = pb
	}

	return &messagev1.ListMessagesResponse{
		Messages: pbMessages,
	}, nil
}

func (h *Handler) PinMessage(ctx context.Context, req *messagev1.PinMessageRequest) (*messagev1.PinMessageResponse, error) {
	userID := middleware.UserIDFromContext(ctx)

	if err := h.svc.PinMessage(ctx, userID, req.ChannelId, h.resolveGuildID(ctx, req.ChannelId), req.MessageId); err != nil {
		return nil, mapError(err)
	}

	return &messagev1.PinMessageResponse{}, nil
}

func (h *Handler) UnpinMessage(ctx context.Context, req *messagev1.UnpinMessageRequest) (*messagev1.UnpinMessageResponse, error) {
	userID := middleware.UserIDFromContext(ctx)

	if err := h.svc.UnpinMessage(ctx, userID, req.ChannelId, h.resolveGuildID(ctx, req.ChannelId), req.MessageId); err != nil {
		return nil, mapError(err)
	}

	return &messagev1.UnpinMessageResponse{}, nil
}

func (h *Handler) AddReaction(ctx context.Context, req *messagev1.AddReactionRequest) (*messagev1.AddReactionResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.svc.AddReaction(ctx, userID, req.ChannelId, h.resolveGuildID(ctx, req.ChannelId), req.MessageId, req.Emoji); err != nil {
		return nil, mapError(err)
	}

	return &messagev1.AddReactionResponse{}, nil
}

func (h *Handler) RemoveReaction(ctx context.Context, req *messagev1.RemoveReactionRequest) (*messagev1.RemoveReactionResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.svc.RemoveReaction(ctx, userID, req.ChannelId, h.resolveGuildID(ctx, req.ChannelId), req.MessageId, req.Emoji); err != nil {
		return nil, mapError(err)
	}

	return &messagev1.RemoveReactionResponse{}, nil
}

func (h *Handler) AckMessage(ctx context.Context, req *messagev1.AckMessageRequest) (*messagev1.AckMessageResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.svc.AckMessage(ctx, userID, req.ChannelId, h.resolveGuildID(ctx, req.ChannelId), req.MessageId); err != nil {
		return nil, mapError(err)
	}

	return &messagev1.AckMessageResponse{}, nil
}

func (h *Handler) StartTyping(ctx context.Context, req *messagev1.StartTypingRequest) (*messagev1.StartTypingResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.svc.StartTyping(ctx, req.UserName, userID, req.ChannelId, h.resolveGuildID(ctx, req.ChannelId)); err != nil {
		return nil, mapError(err)
	}

	return &messagev1.StartTypingResponse{}, nil
}

func (h *Handler) messageToProto(ctx context.Context, msg *Message, currentUserID string) (*messagev1.Message, error) {
	var zeroUUID [16]byte
	msgID := uuidValue(msg.Id)
	channelID := uuidValue(msg.ChannelId)
	authorID := uuidValue(msg.AuthorId)
	replyToID := uuidValue(msg.ReplyToId)
	forwardedFromChannelID := uuidValue(msg.ForwardedFromChannelId)
	forwardedFromMessageID := uuidValue(msg.ForwardedFromMessageId)
	forwardedFromAuthorID := uuidValue(msg.ForwardedFromAuthorId)
	mentions := uuidSliceValue(msg.MentionUserIds)

	pb := &messagev1.Message{
		Id:        msgID.String(),
		ChannelId: channelID.String(),
		AuthorId:  authorID.String(),
		Content:   stringValue(msg.Content),
		Type:      messagev1.MessageType(int64Value(msg.Type)),
		Pinned:    boolValue(msg.Pinned),
		CreatedAt: timestamppb.New(timeValue(msg.CreatedAt)),
	}

	if replyToID != gocql.UUID(zeroUUID) {
		pb.ReplyToId = replyToID.String()
	}
	if msg.EditedAt != nil {
		pb.EditedAt = timestamppb.New(*msg.EditedAt)
	}
	if forwardedFromMessageID != gocql.UUID(zeroUUID) {
		pb.ForwardedFrom = &messagev1.ForwardedReference{
			ChannelId: forwardedFromChannelID.String(),
			MessageId: forwardedFromMessageID.String(),
			AuthorId:  forwardedFromAuthorID.String(),
		}
	}
	if len(mentions) > 0 {
		pb.MentionUserIds = make([]string, len(mentions))
		for i, m := range mentions {
			pb.MentionUserIds[i] = m.String()
		}
	}

	reactions, err := h.svc.GetReactions(ctx, channelID.String(), msgID.String(), currentUserID)
	if err == nil && len(reactions) > 0 {
		pb.Reactions = make([]*messagev1.Reaction, len(reactions))
		for i, r := range reactions {
			pb.Reactions[i] = &messagev1.Reaction{
				Emoji: r.Emoji,
				Count: r.Count,
				Me:    r.Me,
			}
		}
	}

	atts, err := h.svc.GetAttachments(ctx, channelID.String(), msgID.String())
	if err == nil {
		pb.Attachments = attachmentsToProto(atts)
	}

	return pb, nil
}

func attachmentsToProto(atts []Attachment) []*messagev1.Attachment {
	if len(atts) == 0 {
		return nil
	}
	out := make([]*messagev1.Attachment, len(atts))
	for i, a := range atts {
		out[i] = &messagev1.Attachment{
			Id:          uuidValue(a.Id).String(),
			Filename:    stringValue(a.Filename),
			Url:         stringValue(a.Url),
			ContentType: stringValue(a.ContentType),
			Size:        int64Value(a.Size),
		}
	}
	return out
}

func (h *Handler) SendDirectMessage(ctx context.Context, req *messagev1.SendDirectMessageRequest) (*messagev1.SendDirectMessageResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	channelID, msg, err := h.svc.SendDirectMessage(ctx, userID, req.RecipientId, req.Content)
	if err != nil {
		return nil, mapError(err)
	}

	pbMsg, err := h.messageToProto(ctx, msg, userID)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to build response")
	}

	return &messagev1.SendDirectMessageResponse{
		ChannelId: channelID,
		Message:   pbMsg,
	}, nil
}

func (h *Handler) GetUnreadCounts(ctx context.Context, req *messagev1.GetUnreadCountsRequest) (*messagev1.GetUnreadCountsResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	unreads, total, err := h.svc.GetUnreadCounts(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}

	var dms []*messagev1.UnreadDM
	var chMsgs []*messagev1.UnreadChannelMessage

	for _, u := range unreads {
		var pbMsgs []*messagev1.Message
		for _, m := range u.RecentMessages {
			pb, _ := h.messageToProto(ctx, m, userID)
			if pb != nil {
				pbMsgs = append(pbMsgs, pb)
			}
		}

		if u.IsDM {
			dms = append(dms, &messagev1.UnreadDM{
				ChannelId:         u.ChannelID,
				SenderId:          u.SenderID,
				SenderName:        u.SenderName,
				UnreadCount:       u.UnreadCount,
				MentionCount:      u.MentionCount,
				LastReadMessageId: u.LastReadMsgID,
				Messages:          pbMsgs,
				LastMessageAt:     timestamppb.New(u.LastMessageTime),
			})
		} else {
			chMsgs = append(chMsgs, &messagev1.UnreadChannelMessage{
				ChannelId:         u.ChannelID,
				GuildId:           u.GuildID,
				ChannelName:       u.ChannelName,
				UnreadCount:       u.UnreadCount,
				MentionCount:      u.MentionCount,
				LastReadMessageId: u.LastReadMsgID,
				Messages:          pbMsgs,
				LastMessageAt:     timestamppb.New(u.LastMessageTime),
			})
		}
	}

	return &messagev1.GetUnreadCountsResponse{
		DmMessages:      dms,
		ChannelMessages: chMsgs,
		TotalUnread:     total,
	}, nil
}

func (h *Handler) SearchMessages(ctx context.Context, req *messagev1.SearchMessagesRequest) (*messagev1.SearchMessagesResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	msgs, err := h.svc.SearchMessages(ctx, req.ChannelId, req.Query, req.Limit)
	if err != nil {
		return nil, mapError(err)
	}
	var pb []*messagev1.Message
	for _, m := range msgs {
		p, _ := h.messageToProto(ctx, m, userID)
		pb = append(pb, p)
	}
	return &messagev1.SearchMessagesResponse{Messages: pb, Total: int32(len(pb))}, nil
}

func (h *Handler) GetThreadMessages(ctx context.Context, req *messagev1.GetThreadMessagesRequest) (*messagev1.GetThreadMessagesResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	msgs, err := h.svc.GetThreadMessages(ctx, req.ChannelId, req.ParentMessageId, req.Limit)
	if err != nil {
		return nil, mapError(err)
	}
	var pb []*messagev1.Message
	for _, m := range msgs {
		p, _ := h.messageToProto(ctx, m, userID)
		pb = append(pb, p)
	}
	return &messagev1.GetThreadMessagesResponse{Messages: pb}, nil
}

func (h *Handler) BulkDeleteMessages(ctx context.Context, req *messagev1.BulkDeleteMessagesRequest) (*messagev1.BulkDeleteMessagesResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	count, err := h.svc.BulkDeleteMessages(ctx, userID, req.ChannelId, "", req.MessageIds)
	if err != nil {
		return nil, mapError(err)
	}
	return &messagev1.BulkDeleteMessagesResponse{DeletedCount: int32(count)}, nil
}

func (h *Handler) GetEditHistory(ctx context.Context, req *messagev1.GetEditHistoryRequest) (*messagev1.GetEditHistoryResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	edits, err := h.svc.GetEditHistory(ctx, req.ChannelId, req.MessageId)
	if err != nil {
		return nil, mapError(err)
	}
	var pb []*messagev1.MessageEdit
	for _, e := range edits {
		pb = append(pb, &messagev1.MessageEdit{Content: stringValue(e.OldContent), EditedAt: timestamppb.New(timeValue(e.EditedAt))})
	}
	return &messagev1.GetEditHistoryResponse{Edits: pb}, nil
}

func mapError(err error) error {
	switch {
	case errors.Is(err, ErrMessageNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrInvalidUUID),
		errors.Is(err, ErrChannelRequired),
		errors.Is(err, ErrContentRequired),
		errors.Is(err, ErrEmojiRequired),
		errors.Is(err, ErrMessageRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrUserBlocked):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, ErrNotMessageAuthor),
		errors.Is(err, ErrInsufficientPermissions):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
