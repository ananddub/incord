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

type UserDataResolver interface {
	GetUserGuildIDs(ctx context.Context, userID string) ([]string, error)
	GetUserFriendIDs(ctx context.Context, userID string) ([]string, error)
}

type VoiceSnapshotProvider interface {
	GetChannelParticipants(ctx context.Context, channelID string) ([]*streamv1.VoiceStateEvent, error)
	GetGuildVoiceChannelIDs(ctx context.Context, guildID string) ([]string, error)
}

type PresenceController interface {
	OnUserConnect(ctx context.Context, userID string)
	OnUserDisconnect(ctx context.Context, userID string)
}

// ChannelViewer answers "can this user see this channel right now?".
// Used to filter per-channel fan-out (text messages, typing, voice
// state) so a private channel's traffic never reaches a member who
// doesn't have view_channel. Implemented by authz.Client.CanViewChannel.
type ChannelViewer interface {
	CanViewChannel(ctx context.Context, userID, channelID string) bool
}

type Handler struct {
	streamv1.UnimplementedStreamServiceServer
	nats          *realtime.Hub
	resolver      UserDataResolver
	voiceSnapshot VoiceSnapshotProvider
	presence      PresenceController
	viewer        ChannelViewer
}

func NewHandler(nats *realtime.Hub, resolver UserDataResolver) *Handler {
	return &Handler{nats: nats, resolver: resolver}
}

func (h *Handler) SetVoiceSnapshotProvider(v VoiceSnapshotProvider) { h.voiceSnapshot = v }

func (h *Handler) SetPresenceController(p PresenceController) { h.presence = p }

// SetChannelViewer wires the per-channel visibility check used to
// filter guild fan-out. Without it, every guild member receives every
// channel's traffic; with it, private channels stay private.
func (h *Handler) SetChannelViewer(v ChannelViewer) { h.viewer = v }

func streamFromSubjects[T any](h *Handler, ctx context.Context, subjects []string, send func(*T) error) error {
	return streamFromSubjectsFiltered(h, ctx, subjects, send, nil)
}

// streamFromSubjectsFiltered is streamFromSubjects plus an optional
// per-event gate. `eligible` returns true when the decoded event should
// be forwarded to the client. Used to drop messages from channels the
// user can't view (private channel privacy) while still letting the
// same user receive traffic from channels they *can* view on the same
// wildcard subscription.
func streamFromSubjectsFiltered[T any](
	h *Handler, ctx context.Context,
	subjects []string, send func(*T) error,
	eligible func(ctx context.Context, evt *T) bool,
) error {
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
			if json.Unmarshal(msg.Data, &evt) != nil {
				continue
			}
			if eligible != nil && !eligible(ctx, &evt) {
				continue
			}
			if err := send(&evt); err != nil {
				return err
			}
		}
	}
}

func (h *Handler) StreamDmChat(req *streamv1.StreamDmChatRequest, stream streamv1.StreamService_StreamDmChatServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	subjects := []string{realtime.DmAllMessages(userID)}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

func (h *Handler) StreamDmChannels(req *streamv1.StreamDmChannelsRequest, stream streamv1.StreamService_StreamDmChannelsServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	subjects := []string{realtime.DmChannels(userID)}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

func (h *Handler) StreamDmCalls(req *streamv1.StreamDmCallsRequest, stream streamv1.StreamService_StreamDmCallsServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	subjects := []string{realtime.DmCall(userID)}
	return streamFromSubjects(h, stream.Context(), subjects, stream.Send)
}

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
	return streamFromSubjectsFiltered(h, stream.Context(), subjects, stream.Send,
		func(ctx context.Context, e *streamv1.TextChannelEvent) bool {
			return h.canSeeChannel(ctx, userID, e.GetChannelId())
		})
}

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
	return streamFromSubjectsFiltered(h, stream.Context(), subjects, stream.Send,
		func(ctx context.Context, e *streamv1.VoiceChatEvent) bool {
			return h.canSeeChannel(ctx, userID, e.GetChannelId())
		})
}

// canSeeChannel is the per-event viewer gate used by every channel-scoped
// stream. A nil viewer (tests / no authz) passes everything. The result
// is NOT cached — OpenFGA's own cache plus the Check call's sub-ms cost
// make a per-message check acceptable; permission changes take effect
// immediately with no re-subscribe dance.
func (h *Handler) canSeeChannel(ctx context.Context, userID, channelID string) bool {
	if h.viewer == nil || channelID == "" {
		return true
	}
	return h.viewer.CanViewChannel(ctx, userID, channelID)
}

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

func (h *Handler) StreamVoiceState(req *streamv1.StreamVoiceStateRequest, stream streamv1.StreamService_StreamVoiceStateServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	guildIDs, err := h.resolver.GetUserGuildIDs(stream.Context(), userID)
	if err != nil {
		return status.Error(codes.Internal, "failed to get guilds")
	}
	if h.voiceSnapshot != nil {
		for _, gid := range guildIDs {
			channelIDs, err := h.voiceSnapshot.GetGuildVoiceChannelIDs(stream.Context(), gid)
			if err != nil {
				continue
			}
			for _, chID := range channelIDs {
				// Skip channels this user can't see — no snapshot leak
				// for private voice rooms.
				if !h.canSeeChannel(stream.Context(), userID, chID) {
					continue
				}
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
	var subjects []string
	for _, gid := range guildIDs {
		subjects = append(subjects, realtime.GuildAllVoice(gid))
	}
	return streamFromSubjectsFiltered(h, stream.Context(), subjects, stream.Send,
		func(ctx context.Context, e *streamv1.VoiceStateEvent) bool {
			return h.canSeeChannel(ctx, userID, e.GetChannelId())
		})
}

func (h *Handler) StreamTyping(req *streamv1.StreamTypingRequest, stream streamv1.StreamService_StreamTypingServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}
	guildIDs, _ := h.resolver.GetUserGuildIDs(stream.Context(), userID)

	subjects := []string{realtime.DmAllTyping(userID)}
	for _, gid := range guildIDs {
		subjects = append(subjects, realtime.GuildAllTyping(gid))
	}
	return streamFromSubjectsFiltered(h, stream.Context(), subjects, stream.Send,
		func(ctx context.Context, e *streamv1.TypingEvent) bool {
			// DM typing (guild_id == "") is already routed to the
			// per-user subject — only guild typing needs a viewer check.
			if e.GetGuildId() == "" {
				return true
			}
			return h.canSeeChannel(ctx, userID, e.GetChannelId())
		})
}

func (h *Handler) StreamFriendActivity(req *streamv1.StreamFriendActivityRequest, stream streamv1.StreamService_StreamFriendActivityServer) error {
	userID := middleware.UserIDFromContext(stream.Context())
	if userID == "" {
		return status.Error(codes.Unauthenticated, "not authenticated")
	}

	if h.presence != nil {
		h.presence.OnUserConnect(stream.Context(), userID)
		defer func() {
			h.presence.OnUserDisconnect(context.Background(), userID)
		}()
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
