package auth

import (
	"context"
	"errors"
	"strings"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PresenceUpdater sets a user's presence to offline.
type PresenceUpdater interface {
	SetOffline(ctx context.Context, userID string) error
}

type Handler struct {
	authv1.UnimplementedAuthServiceServer
	svc             *Service
	presenceUpdater PresenceUpdater
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// SetPresenceUpdater sets the presence updater (avoids circular deps in constructor).
func (h *Handler) SetPresenceUpdater(p PresenceUpdater) { h.presenceUpdater = p }

func (h *Handler) Register(ctx context.Context, req *authv1.RegisterRequest) (*authv1.RegisterResponse, error) {
	userID, err := h.svc.Register(ctx, req.Username, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrUserExists) {
			return nil, status.Error(codes.AlreadyExists, "user already exists")
		}
		errMsg := err.Error()
		if strings.Contains(errMsg, "duplicate key") || strings.Contains(errMsg, "unique") || strings.Contains(errMsg, "23505") {
			return nil, status.Error(codes.AlreadyExists, "user already exists")
		}
		return nil, status.Error(codes.Internal, "failed to register")
	}

	return &authv1.RegisterResponse{
		UserId:  userID,
		Message: "OTP sent to " + req.Email + ". Please verify to complete registration.",
	}, nil
}

func (h *Handler) VerifyOTP(ctx context.Context, req *authv1.VerifyOTPRequest) (*authv1.VerifyOTPResponse, error) {
	userID, tokens, err := h.svc.VerifyOTP(ctx, req.Email, req.Otp)
	if err != nil {
		if errors.Is(err, ErrInvalidOTP) {
			return nil, status.Error(codes.InvalidArgument, "invalid or expired OTP")
		}
		return nil, status.Error(codes.Internal, "failed to verify OTP")
	}

	return &authv1.VerifyOTPResponse{
		UserId:       userID,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
	}, nil
}

func (h *Handler) ResendOTP(ctx context.Context, req *authv1.ResendOTPRequest) (*authv1.ResendOTPResponse, error) {
	if err := h.svc.ResendOTP(ctx, req.Email); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		return nil, status.Error(codes.Internal, "failed to resend OTP")
	}

	return &authv1.ResendOTPResponse{
		Message: "OTP sent to " + req.Email,
	}, nil
}

func (h *Handler) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	userID, tokens, err := h.svc.Login(ctx, req.Email, req.Password)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}
		if errors.Is(err, ErrNotVerified) {
			return nil, status.Error(codes.FailedPrecondition, "email not verified. Please verify OTP first.")
		}
		return nil, status.Error(codes.Internal, "failed to login")
	}

	return &authv1.LoginResponse{
		UserId:       userID,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
	}, nil
}

func (h *Handler) RefreshToken(ctx context.Context, req *authv1.RefreshTokenRequest) (*authv1.RefreshTokenResponse, error) {
	tokens, err := h.svc.RefreshToken(ctx, req.RefreshToken)
	if err != nil {
		if errors.Is(err, ErrInvalidToken) {
			return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
		}
		return nil, status.Error(codes.Internal, "failed to refresh token")
	}

	return &authv1.RefreshTokenResponse{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
	}, nil
}

func (h *Handler) Logout(ctx context.Context, req *authv1.LogoutRequest) (*authv1.LogoutResponse, error) {
	if err := h.svc.Logout(ctx, req.RefreshToken); err != nil {
		return nil, status.Error(codes.Internal, "failed to logout")
	}

	// Set presence to offline
	if h.presenceUpdater != nil {
		if userID := middleware.UserIDFromContext(ctx); userID != "" {
			if err := h.presenceUpdater.SetOffline(ctx, userID); err != nil {
				logger.Log.Warn().Err(err).Str("user_id", userID).Msg("failed to set offline on logout")
			}
		}
	}

	return &authv1.LogoutResponse{}, nil
}

func (h *Handler) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest) (*authv1.ValidateTokenResponse, error) {
	userID, valid, err := h.svc.ValidateToken(ctx, req.AccessToken)
	if err != nil {
		return nil, status.Error(codes.Internal, "failed to validate token")
	}

	return &authv1.ValidateTokenResponse{
		UserId: userID,
		Valid:  valid,
	}, nil
}
