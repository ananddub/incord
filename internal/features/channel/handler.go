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

func (h *Handler) ListDMChannelMembers(ctx context.Context, req *channelv1.ListDMChannelMembersRequest) (*channelv1.ListDMChannelMembersResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	rows, err := h.svc.ListDMChannelMembers(ctx, callerID, req.GetChannelId())
	if err != nil {
		return nil, mapError(err)
	}

	members := make([]*channelv1.DMMember, len(rows))
	for i, r := range rows {
		members[i] = dmMemberToProto(r)
	}
	return &channelv1.ListDMChannelMembersResponse{Members: members}, nil
}

func (h *Handler) ListDMChannelsWithMembers(ctx context.Context, _ *channelv1.ListDMChannelsWithMembersRequest) (*channelv1.ListDMChannelsWithMembersResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	items, err := h.svc.ListDMChannelsWithMembers(ctx, userID)
	if err != nil {
		return nil, mapError(err)
	}

	out := make([]*channelv1.DMChannelWithMembers, len(items))
	for i, it := range items {
		members := make([]*channelv1.DMMember, len(it.Members))
		for j, m := range it.Members {
			members[j] = dmMemberToProto(m)
		}
		pb := channelToProto(it.Channel)
		out[i] = &channelv1.DMChannelWithMembers{
			Id:        pb.GetId(),
			GuildId:   pb.GetGuildId(),
			Name:      pb.GetName(),
			Type:      pb.GetType(),
			Topic:     pb.GetTopic(),
			Position:  pb.GetPosition(),
			ParentId:  pb.GetParentId(),
			CreatedAt: pb.GetCreatedAt(),
			Members:   members,
		}
	}
	return &channelv1.ListDMChannelsWithMembersResponse{Channels: out}, nil
}

func dmMemberToProto(r db.GetDMChannelMemberProfilesRow) *channelv1.DMMember {
	return &channelv1.DMMember{
		Id:          uuidToString(r.ID),
		Username:    r.Username,
		DisplayName: r.DisplayName,
		AvatarUrl:   r.AvatarUrl,
		Status:      r.Status,
	}
}

func (h *Handler) AddDMGroupMember(ctx context.Context, req *channelv1.AddDMGroupMemberRequest) (*channelv1.AddDMGroupMemberResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.svc.AddDMGroupMember(ctx, callerID, req.GetChannelId(), req.GetUserId()); err != nil {
		return nil, mapError(err)
	}

	return &channelv1.AddDMGroupMemberResponse{}, nil
}

func (h *Handler) RemoveDMGroupMember(ctx context.Context, req *channelv1.RemoveDMGroupMemberRequest) (*channelv1.RemoveDMGroupMemberResponse, error) {
	callerID := middleware.UserIDFromContext(ctx)
	if callerID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}

	if err := h.svc.RemoveDMGroupMember(ctx, callerID, req.GetChannelId(), req.GetUserId()); err != nil {
		return nil, mapError(err)
	}

	return &channelv1.RemoveDMGroupMemberResponse{}, nil
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

func (h *Handler) SetChannelOverride(ctx context.Context, req *channelv1.SetChannelOverrideRequest) (*channelv1.SetChannelOverrideResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	if err := h.svc.SetChannelOverride(ctx, userID, req.GetChannelId(), req.GetTargetType(), req.GetTargetId(), req.GetPermission(), req.GetEffect()); err != nil {
		return nil, mapError(err)
	}
	return &channelv1.SetChannelOverrideResponse{}, nil
}

func (h *Handler) DeleteChannelOverride(ctx context.Context, req *channelv1.DeleteChannelOverrideRequest) (*channelv1.DeleteChannelOverrideResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	if err := h.svc.DeleteChannelOverride(ctx, userID, req.GetChannelId(), req.GetTargetType(), req.GetTargetId(), req.GetPermission()); err != nil {
		return nil, mapError(err)
	}
	return &channelv1.DeleteChannelOverrideResponse{}, nil
}

func (h *Handler) ListChannelOverrides(ctx context.Context, req *channelv1.ListChannelOverridesRequest) (*channelv1.ListChannelOverridesResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "not authenticated")
	}
	rows, err := h.svc.ListChannelOverrides(ctx, userID, req.GetChannelId())
	if err != nil {
		return nil, mapError(err)
	}
	out := make([]*channelv1.ChannelOverride, len(rows))
	for i, r := range rows {
		out[i] = &channelv1.ChannelOverride{
			TargetType: r.TargetType,
			TargetId:   r.TargetID,
			Permission: r.Permission,
			Effect:     r.Effect,
		}
	}
	return &channelv1.ListChannelOverridesResponse{Overrides: out}, nil
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
	case errors.Is(err, ErrNotGroupDM):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ErrNotGuildMember),
		errors.Is(err, ErrInsufficientPermissions),
		errors.Is(err, ErrNotDMChannelMember):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, ErrAlreadyDMChannelMember):
		return status.Error(codes.AlreadyExists, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}
