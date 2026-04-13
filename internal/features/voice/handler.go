package voice

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

// Handler implements the VoiceServiceServer gRPC interface.
type Handler struct {
	voicev1.UnimplementedVoiceServiceServer
	service *Service
}

// NewHandler creates a new voice Handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// JoinChannel allows a user to join a voice channel.
func (h *Handler) JoinChannel(ctx context.Context, req *voicev1.JoinChannelRequest) (*voicev1.JoinChannelResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

result, err := h.service.JoinChannel(ctx, userID, req.GetGuildId(), req.GetChannelId())
	if err != nil {
		if errors.Is(err, ErrInsufficientPermissions) {
			return nil, status.Error(codes.PermissionDenied, err.Error())
		}
		return nil, status.Errorf(codes.Internal, "failed to join channel: %v", err)
	}

	return &voicev1.JoinChannelResponse{
		SessionId:     result.SessionID,
		UdpEndpoint:   result.UDPEndpoint,
		UdpPort:       result.UDPPort,
		EncryptionKey: result.EncryptionKey,
		Ssrc:          result.SSRC,
	}, nil
}

// LeaveChannel allows a user to leave a voice channel.
func (h *Handler) LeaveChannel(ctx context.Context, req *voicev1.LeaveChannelRequest) (*voicev1.LeaveChannelResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

if err := h.service.LeaveChannel(ctx, userID, req.GetChannelId()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to leave channel: %v", err)
	}

	return &voicev1.LeaveChannelResponse{}, nil
}

// GetChannelParticipants returns all participants in a voice channel.
func (h *Handler) GetChannelParticipants(ctx context.Context, req *voicev1.GetChannelParticipantsRequest) (*voicev1.GetChannelParticipantsResponse, error) {
participants, err := h.service.GetChannelParticipants(ctx, req.GetChannelId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get participants: %v", err)
	}

	return &voicev1.GetChannelParticipantsResponse{
		Participants: participants,
	}, nil
}

// UpdateVoiceState updates the calling user's voice state in a channel.
func (h *Handler) UpdateVoiceState(ctx context.Context, req *voicev1.UpdateVoiceStateRequest) (*voicev1.UpdateVoiceStateResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

if err := h.service.UpdateVoiceState(ctx, userID, req.GetChannelId(), req.GetSelfMute(), req.GetSelfDeaf(), req.GetVideo(), req.GetStream()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update voice state: %v", err)
	}

	return &voicev1.UpdateVoiceStateResponse{}, nil
}

