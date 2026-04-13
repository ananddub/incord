package presence

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

// Handler implements the PresenceServiceServer gRPC interface.
type Handler struct {
	presencev1.UnimplementedPresenceServiceServer
	service *Service
}

// NewHandler creates a new presence Handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// UpdatePresence updates the calling user's presence.
func (h *Handler) UpdatePresence(ctx context.Context, req *presencev1.UpdatePresenceRequest) (*presencev1.UpdatePresenceResponse, error) {
	userID := middleware.UserIDFromContext(ctx)
	if userID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user id")
	}

	p, err := h.service.UpdatePresence(ctx, userID, req.GetStatus(), req.GetCustomStatus())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update presence: %v", err)
	}

	return &presencev1.UpdatePresenceResponse{
		Presence: p,
	}, nil
}

// GetPresence returns the presence for a specific user.
func (h *Handler) GetPresence(ctx context.Context, req *presencev1.GetPresenceRequest) (*presencev1.GetPresenceResponse, error) {
	p, err := h.service.GetPresence(ctx, req.GetUserId())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get presence: %v", err)
	}

	return &presencev1.GetPresenceResponse{
		Presence: p,
	}, nil
}

// GetBulkPresence returns presence for multiple users.
func (h *Handler) GetBulkPresence(ctx context.Context, req *presencev1.GetBulkPresenceRequest) (*presencev1.GetBulkPresenceResponse, error) {
	presences, err := h.service.GetBulkPresence(ctx, req.GetUserIds())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get bulk presence: %v", err)
	}

	return &presencev1.GetBulkPresenceResponse{
		Presences: presences,
	}, nil
}
