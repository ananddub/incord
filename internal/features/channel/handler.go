package channel

import (
	"context"
	"errors"

	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	"github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Handler struct {
	channelv1.UnimplementedChannelServiceServer
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) CreateChannel(ctx context.Context, req *channelv1.CreateChannelRequest) (*channelv1.CreateChannelResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "channel name is required")
	}

	ch, err := h.svc.CreateChannel(ctx, userID, req.GuildId, req.Name, int32(req.Type), req.Topic, req.ParentId)
	if err != nil {
		return nil, mapError(err)
	}

	return &channelv1.CreateChannelResponse{
		Channel: channelToProto(ch),
	}, nil
}

func (h *Handler) GetChannel(ctx context.Context, req *channelv1.GetChannelRequest) (*channelv1.GetChannelResponse, error) {
	if req.ChannelId == "" {
		return nil, status.Error(codes.InvalidArgument, "channel_id is required")
	}

	ch, err := h.svc.GetChannel(ctx, req.ChannelId)
	if err != nil {
		return nil, mapError(err)
	}

	return &channelv1.GetChannelResponse{
		Channel: channelToProto(ch),
	}, nil
}

func (h *Handler) UpdateChannel(ctx context.Context, req *channelv1.UpdateChannelRequest) (*channelv1.UpdateChannelResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if req.ChannelId == "" {
		return nil, status.Error(codes.InvalidArgument, "channel_id is required")
	}

	ch, err := h.svc.UpdateChannel(ctx, userID, req.ChannelId, req.Name, req.Topic, req.Position, req.ParentId)
	if err != nil {
		return nil, mapError(err)
	}

	return &channelv1.UpdateChannelResponse{
		Channel: channelToProto(ch),
	}, nil
}

func (h *Handler) DeleteChannel(ctx context.Context, req *channelv1.DeleteChannelRequest) (*channelv1.DeleteChannelResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if req.ChannelId == "" {
		return nil, status.Error(codes.InvalidArgument, "channel_id is required")
	}

	if err := h.svc.DeleteChannel(ctx, userID, req.ChannelId); err != nil {
		return nil, mapError(err)
	}

	return &channelv1.DeleteChannelResponse{}, nil
}

func (h *Handler) ListGuildChannels(ctx context.Context, req *channelv1.ListGuildChannelsRequest) (*channelv1.ListGuildChannelsResponse, error) {
	if req.GuildId == "" {
		return nil, status.Error(codes.InvalidArgument, "guild_id is required")
	}

	channels, err := h.svc.ListGuildChannels(ctx, req.GuildId)
	if err != nil {
		return nil, mapError(err)
	}

	pbChannels := make([]*channelv1.Channel, len(channels))
	for i, ch := range channels {
		pbChannels[i] = channelToProto(ch)
	}

	return &channelv1.ListGuildChannelsResponse{
		Channels: pbChannels,
	}, nil
}

func (h *Handler) CreateDMChannel(ctx context.Context, req *channelv1.CreateDMChannelRequest) (*channelv1.CreateDMChannelResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if len(req.RecipientIds) == 0 {
		return nil, status.Error(codes.InvalidArgument, "at least one recipient is required")
	}

	ch, err := h.svc.CreateDMChannel(ctx, userID, req.RecipientIds)
	if err != nil {
		return nil, mapError(err)
	}

	return &channelv1.CreateDMChannelResponse{
		Channel: channelToProto(ch),
	}, nil
}

func (h *Handler) ListDMChannels(ctx context.Context, _ *channelv1.ListDMChannelsRequest) (*channelv1.ListDMChannelsResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	channels, err := h.svc.ListDMChannels(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}

	pbChannels := make([]*channelv1.Channel, len(channels))
	for i, ch := range channels {
		pbChannels[i] = channelToProto(ch)
	}

	return &channelv1.ListDMChannelsResponse{
		Channels: pbChannels,
	}, nil
}

// channelToProto converts a db.Channel to the proto Channel message.
func channelToProto(ch db.Channel) *channelv1.Channel {
	pb := &channelv1.Channel{
		Id:       uuidToString(ch.ID),
		Name:     ch.Name,
		Type:     channelv1.ChannelType(ch.Type),
		Topic:    ch.Topic,
		Position: ch.Position,
	}

	if ch.GuildID.Valid {
		pb.GuildId = uuidToString(ch.GuildID)
	}
	if ch.ParentID.Valid {
		pb.ParentId = uuidToString(ch.ParentID)
	}
	if ch.CreatedAt.Valid {
		pb.CreatedAt = timestamppb.New(ch.CreatedAt.Time)
	}

	return pb
}

// mapError converts domain errors to gRPC status errors.
func mapError(err error) error {
	switch {
	case errors.Is(err, ErrChannelNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, ErrGuildIDRequired),
		errors.Is(err, ErrNameRequired),
		errors.Is(err, ErrInvalidChannelType),
		errors.Is(err, ErrRecipientRequired),
		errors.Is(err, ErrInvalidUUID):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrNotGuildMember),
		errors.Is(err, ErrInsufficientPermissions):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
