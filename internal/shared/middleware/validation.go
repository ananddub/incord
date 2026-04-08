package middleware

import (
	"context"
	"net/mail"
	"strings"
	"unicode/utf8"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ValidationInterceptor returns a gRPC unary interceptor that validates
// field lengths and formats on known request types before they reach handlers.
func ValidationInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if err := validateRequest(req); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

func validateRequest(req any) error {
	switch r := req.(type) {

	// ── Auth ────────────────────────────────────────────────────────────
	case *authv1.RegisterRequest:
		if err := validateUsername(r.GetUsername()); err != nil {
			return err
		}
		if err := validateEmail(r.GetEmail()); err != nil {
			return err
		}
		if err := validatePassword(r.GetPassword()); err != nil {
			return err
		}

	case *authv1.LoginRequest:
		if err := validateEmail(r.GetEmail()); err != nil {
			return err
		}
		if err := validatePassword(r.GetPassword()); err != nil {
			return err
		}

	// ── User ────────────────────────────────────────────────────────────
	case *userv1.UpdateUserRequest:
		if r.Username != nil {
			if err := validateUsername(r.GetUsername()); err != nil {
				return err
			}
		}
		if r.Bio != nil {
			if err := validateBio(r.GetBio()); err != nil {
				return err
			}
		}

	// ── Message ─────────────────────────────────────────────────────────
	case *messagev1.SendMessageRequest:
		if err := validateMessageContent(r.GetContent()); err != nil {
			return err
		}

	case *messagev1.EditMessageRequest:
		if err := validateMessageContent(r.GetContent()); err != nil {
			return err
		}

	case *messagev1.SendDirectMessageRequest:
		if err := validateMessageContent(r.GetContent()); err != nil {
			return err
		}

	// ── Guild ───────────────────────────────────────────────────────────
	case *guildv1.CreateGuildRequest:
		if err := validateGuildName(r.GetName()); err != nil {
			return err
		}

	case *guildv1.UpdateGuildRequest:
		if r.Name != nil {
			if err := validateGuildName(r.GetName()); err != nil {
				return err
			}
		}

	case *guildv1.CreateRoleRequest:
		if err := validateRoleName(r.GetName()); err != nil {
			return err
		}

	case *guildv1.UpdateRoleRequest:
		if r.Name != nil {
			if err := validateRoleName(r.GetName()); err != nil {
				return err
			}
		}

	// ── Channel ─────────────────────────────────────────────────────────
	case *channelv1.CreateChannelRequest:
		if err := validateChannelName(r.GetName()); err != nil {
			return err
		}

	case *channelv1.UpdateChannelRequest:
		if r.Name != nil {
			if err := validateChannelName(r.GetName()); err != nil {
				return err
			}
		}
	}

	return nil
}

// ── Field validators ────────────────────────────────────────────────────────

func validateUsername(v string) error {
	n := utf8.RuneCountInString(v)
	if n < 2 || n > 32 {
		return status.Errorf(codes.InvalidArgument, "username must be between 2 and 32 characters")
	}
	return nil
}

func validateEmail(v string) error {
	v = strings.TrimSpace(v)
	if utf8.RuneCountInString(v) > 255 {
		return status.Errorf(codes.InvalidArgument, "email must be at most 255 characters")
	}
	if _, err := mail.ParseAddress(v); err != nil {
		return status.Errorf(codes.InvalidArgument, "email format is invalid")
	}
	return nil
}

func validatePassword(v string) error {
	if utf8.RuneCountInString(v) < 6 {
		return status.Errorf(codes.InvalidArgument, "password must be at least 6 characters")
	}
	return nil
}

func validateMessageContent(v string) error {
	if utf8.RuneCountInString(v) > 4000 {
		return status.Errorf(codes.InvalidArgument, "message content must be at most 4000 characters")
	}
	return nil
}

func validateBio(v string) error {
	if utf8.RuneCountInString(v) > 500 {
		return status.Errorf(codes.InvalidArgument, "bio must be at most 500 characters")
	}
	return nil
}

func validateGuildName(v string) error {
	n := utf8.RuneCountInString(v)
	if n < 2 || n > 100 {
		return status.Errorf(codes.InvalidArgument, "guild name must be between 2 and 100 characters")
	}
	return nil
}

func validateChannelName(v string) error {
	n := utf8.RuneCountInString(v)
	if n < 1 || n > 100 {
		return status.Errorf(codes.InvalidArgument, "channel name must be between 1 and 100 characters")
	}
	return nil
}

func validateRoleName(v string) error {
	n := utf8.RuneCountInString(v)
	if n < 1 || n > 100 {
		return status.Errorf(codes.InvalidArgument, "role name must be between 1 and 100 characters")
	}
	return nil
}
