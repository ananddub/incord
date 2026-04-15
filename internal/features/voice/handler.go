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

// JoinChannel returns the LiveKit URL + JWT the client should use to connect
// directly to the SFU.
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
		Url:       result.URL,
		Token:     result.Token,
		Room:      result.Room,
		ExpiresIn: result.ExpiresIn,
	}, nil
}

// LeaveChannel tells LiveKit to evict the caller from the room.
func (h *Handler) LeaveChannel(ctx context.Context, req *voicev1.LeaveChannelRequest) (*voicev1.LeaveChannelResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

	if err := h.service.LeaveChannel(ctx, userID, req.GetGuildId(), req.GetChannelId()); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to leave channel: %v", err)
	}

	return &voicev1.LeaveChannelResponse{}, nil
}

// StartDMCall initiates a voice/video call inside a DM channel.
func (h *Handler) StartDMCall(ctx context.Context, req *voicev1.StartDMCallRequest) (*voicev1.StartDMCallResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

	result, err := h.service.StartDMCall(ctx, userID, req.GetChannelId(), req.GetVideo())
	if err != nil {
		return nil, mapDMCallError(err)
	}
	return &voicev1.StartDMCallResponse{
		Url:       result.URL,
		Token:     result.Token,
		Room:      result.Room,
		ExpiresIn: result.ExpiresIn,
	}, nil
}

// JoinDMCall accepts a ringing DM call.
func (h *Handler) JoinDMCall(ctx context.Context, req *voicev1.JoinDMCallRequest) (*voicev1.JoinDMCallResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

	result, err := h.service.JoinDMCall(ctx, userID, req.GetChannelId(), req.GetVideo())
	if err != nil {
		return nil, mapDMCallError(err)
	}
	return &voicev1.JoinDMCallResponse{
		Url:       result.URL,
		Token:     result.Token,
		Room:      result.Room,
		ExpiresIn: result.ExpiresIn,
	}, nil
}

// RejectDMCall declines a ringing DM call.
func (h *Handler) RejectDMCall(ctx context.Context, req *voicev1.RejectDMCallRequest) (*voicev1.RejectDMCallResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

	if err := h.service.RejectDMCall(ctx, userID, req.GetChannelId()); err != nil {
		return nil, mapDMCallError(err)
	}
	return &voicev1.RejectDMCallResponse{}, nil
}

// LeaveDMCall hangs up an in-progress DM call.
func (h *Handler) LeaveDMCall(ctx context.Context, req *voicev1.LeaveDMCallRequest) (*voicev1.LeaveDMCallResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

	if err := h.service.LeaveDMCall(ctx, userID, req.GetChannelId()); err != nil {
		return nil, mapDMCallError(err)
	}
	return &voicev1.LeaveDMCallResponse{}, nil
}

func mapDMCallError(err error) error {
	switch {
	case errors.Is(err, ErrNotDMMember):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, ErrDMResolverMissing), errors.Is(err, ErrLiveKitUnavailable):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Errorf(codes.Internal, "dm call failed: %v", err)
	}
}

// GetChannelParticipants returns the LiveKit participants for a voice channel.
func (h *Handler) GetChannelParticipants(ctx context.Context, req *voicev1.GetChannelParticipantsRequest) (*voicev1.GetChannelParticipantsResponse, error) {
	participants, err := h.service.GetChannelParticipants(ctx, req.GetChannelId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get participants: %v", err)
	}

	return &voicev1.GetChannelParticipantsResponse{
		Participants: participants,
	}, nil
}
