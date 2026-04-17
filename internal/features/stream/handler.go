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

// UserDataResolver provides the user's guild memberships and friend list for
// stream subscriptions.
type UserDataResolver interface {
	GetUserGuildIDs(ctx context.Context, userID string) ([]string, error)
	GetUserFriendIDs(ctx context.Context, userID string) ([]string, error)
}

// VoiceSnapshotProvider loads the current voice state for initial sync when
// a client subscribes to StreamVoiceState.
type VoiceSnapshotProvider interface {
	GetChannelParticipants(ctx context.Context, channelID string) ([]*streamv1.VoiceStateEvent, error)
	GetGuildVoiceChannelIDs(ctx context.Context, guildID string) ([]string, error)
}

type Handler struct {
	streamv1.UnimplementedStreamServiceServer
	nats          *realtime.Hub
	resolver      UserDataResolver
	voiceSnapshot VoiceSnapshotProvider
}

func NewHandler(nats *realtime.Hub, resolver UserDataResolver) *Handler {
	return &Handler{nats: nats, resolver: resolver}
}

// SetVoiceSnapshotProvider wires the voice snapshot loader.
func (h *Handler) SetVoiceSnapshotProvider(v VoiceSnapshotProvider) { h.voiceSnapshot = v }

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

// StreamDmChannels - DM channel lifecycle events (create/update/delete)
// for every channel the caller is a member of.
func (h *Handler) StreamDmChannels(req *streamv1.StreamDmChannelsRequest, stream streamv1.StreamService_StreamDmChannelsServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	subjects := []string{realtime.DmChannels(userID)}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

// StreamDmCalls - DM voice-call signalling events (ring, accept, reject,
// end, participant join/leave) for every DM channel the caller is in.
func (h *Handler) StreamDmCalls(req *streamv1.StreamDmCallsRequest, stream streamv1.StreamService_StreamDmCallsServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	subjects := []string{realtime.DmCall(userID)}
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

// StreamVoiceState - voice join/leave/mute/video across all guilds.
// On connect, sends the full current state of every active voice participant
// across the user's guilds so the client starts with a complete picture.
// Then streams incremental updates.
func (h *Handler) StreamVoiceState(req *streamv1.StreamVoiceStateRequest, stream streamv1.StreamService_StreamVoiceStateServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	guildIDs, err := h.resolver.GetUserGuildIDs(stream.Context(), userID)
	if err != nil {
		return status.Error(codes.Internal, "failed to get guilds")
	}
	// Send initial snapshot of all current voice participants so the
	// client renders the correct state immediately on subscribe.
	if h.voiceSnapshot != nil {
		for _, gid := range guildIDs {
			channelIDs, err := h.voiceSnapshot.GetGuildVoiceChannelIDs(stream.Context(), gid)
			if err != nil {
				continue
			}
			for _, chID := range channelIDs {
				events, err := h.voiceSnapshot.GetChannelParticipants(stream.Context(), chID)
				if err != nil {
					continue
				}
				for _, evt := range events {
					evt.GuildId = gid
					if err := stream.Send(evt); err != nil {
						return err
					}
				}
			}
		}
	}

	// Then stream incremental updates
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

// StreamFriendActivity - presence + profile updates + friend requests.
//
// The caller listens on:
//   - their OWN subject — to receive targeted events like "X sent you a friend
//     request" or "X accepted your request"
//   - each FRIEND's subject — to receive ambient events like presence/profile
//     updates that a friend publishes to their own subject
//
// When the friend list changes mid-stream, the client is expected to reconnect.
func (h *Handler) StreamFriendActivity(req *streamv1.StreamFriendActivityRequest, stream streamv1.StreamService_StreamFriendActivityServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}

	subjects := []string{realtime.FriendActivity(userID)}
	if h.resolver != nil {
		friendIDs, err := h.resolver.GetUserFriendIDs(stream.Context(), userID)
		if err == nil {
			for _, fid := range friendIDs {
				subjects = append(subjects, realtime.FriendActivity(fid))
			}
		}
	}

	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}
