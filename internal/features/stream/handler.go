package stream

import (
	"context"
	"encoding/json"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

// UserDataResolver provides the user's guild memberships for stream subscriptions.
type UserDataResolver interface {
	GetUserGuildIDs(ctx context.Context, userID string) ([]string, error)
}

type Handler struct {
	streamv1.UnimplementedStreamServiceServer
	nats     *realtime.Hub
	resolver UserDataResolver
}

func NewHandler(nats *realtime.Hub, resolver UserDataResolver) *Handler {
	return &Handler{nats: nats, resolver: resolver}
}

// helper: subscribe to multiple subjects, forward decoded events to gRPC stream
func streamFromSubjects[T any](h *Handler, ctx context.Context, subjects []string, send func(*T) error) error {
	if len(subjects) == 0 {
		<-ctx.Done()
		return nil
	}
	multi, err := h.nats.SubscribeMulti(subjects)
	if err != nil {
		return status.Error(codes.Internal, "failed to subscribe")
	}
	defer multi.Unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-multi.Ch:
			if !ok {
				return nil
			}
			var evt T
			if json.Unmarshal(msg.Data, &evt) == nil {
				if err := send(&evt); err != nil {
					return err
				}
			}
		}
	}
}

// StreamDmChat - all DM messages across all user's DM channels
func (h *Handler) StreamDmChat(req *streamv1.StreamDmChatRequest, stream streamv1.StreamService_StreamDmChatServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	// Single wildcard subscription for all DM messages
	subjects := []string{realtime.DmAllMessages(userID)}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

// StreamTextChannels - all text messages across all guilds user is in
func (h *Handler) StreamTextChannels(req *streamv1.StreamTextChannelsRequest, stream streamv1.StreamService_StreamTextChannelsServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	guildIDs, err := h.resolver.GetUserGuildIDs(stream.Context(), userID)
	if err != nil {
		return status.Error(codes.Internal, "failed to get guilds")
	}
	var subjects []string
	for _, gid := range guildIDs {
		subjects = append(subjects, realtime.GuildAllMessages(gid))
	}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

// StreamVoiceChat - text chat in voice channels across all guilds
func (h *Handler) StreamVoiceChat(req *streamv1.StreamVoiceChatRequest, stream streamv1.StreamService_StreamVoiceChatServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	guildIDs, err := h.resolver.GetUserGuildIDs(stream.Context(), userID)
	if err != nil {
		return status.Error(codes.Internal, "failed to get guilds")
	}
	var subjects []string
	for _, gid := range guildIDs {
		subjects = append(subjects, realtime.GuildAllVoiceChat(gid))
	}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

// StreamGuildEvents - events across all guilds user is in
func (h *Handler) StreamGuildEvents(req *streamv1.StreamGuildEventsRequest, stream streamv1.StreamService_StreamGuildEventsServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	guildIDs, err := h.resolver.GetUserGuildIDs(stream.Context(), userID)
	if err != nil {
		return status.Error(codes.Internal, "failed to get guilds")
	}
	var subjects []string
	for _, gid := range guildIDs {
		subjects = append(subjects, realtime.GuildEvents(gid))
	}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

// StreamVoiceState - voice join/leave across all guilds
func (h *Handler) StreamVoiceState(req *streamv1.StreamVoiceStateRequest, stream streamv1.StreamService_StreamVoiceStateServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	guildIDs, err := h.resolver.GetUserGuildIDs(stream.Context(), userID)
	if err != nil {
		return status.Error(codes.Internal, "failed to get guilds")
	}
	var subjects []string
	for _, gid := range guildIDs {
		subjects = append(subjects, realtime.GuildAllVoice(gid))
	}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

// StreamTyping - typing across all channels user is in
func (h *Handler) StreamTyping(req *streamv1.StreamTypingRequest, stream streamv1.StreamService_StreamTypingServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	guildIDs, _ := h.resolver.GetUserGuildIDs(stream.Context(), userID)

	// DM typing wildcard + per-guild typing wildcards
	subjects := []string{realtime.DmAllTyping(userID)}
	for _, gid := range guildIDs {
		subjects = append(subjects, realtime.GuildAllTyping(gid))
	}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

// StreamFriendActivity - presence + profile updates
func (h *Handler) StreamFriendActivity(req *streamv1.StreamFriendActivityRequest, stream streamv1.StreamService_StreamFriendActivityServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	sub, err := h.nats.Subscribe(realtime.FriendActivity(userID))
	if err != nil {
		return status.Error(codes.Internal, "failed to subscribe")
	}
	defer sub.Unsubscribe()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case data, ok := <-sub.Ch:
			if !ok {
				return nil
			}
			var evt streamv1.FriendActivityEvent
			if json.Unmarshal(data, &evt) == nil {
				stream.Send(&evt)
			}
		}
	}
}
