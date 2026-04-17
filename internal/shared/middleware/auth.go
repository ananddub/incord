package middleware

import (
	"context"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type contextKey string

const UserIDKey contextKey = "user_id"

// Public methods that don't require auth
var publicMethods = map[string]bool{
	"/auth.v1.AuthService/Register":      true,
	"/auth.v1.AuthService/VerifyOTP":     true,
	"/auth.v1.AuthService/ResendOTP":     true,
	"/auth.v1.AuthService/Login":         true,
	"/auth.v1.AuthService/RefreshToken":  true,
	"/auth.v1.AuthService/ValidateToken": true,
	"/guild.v1.GuildService/PreviewInvite": true,
}

func AuthInterceptor(secret string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip auth for public methods and gRPC reflection
		if publicMethods[info.FullMethod] || strings.HasPrefix(info.FullMethod, "/grpc.reflection.") || strings.HasPrefix(info.FullMethod, "/grpc.health.") {
			return handler(ctx, req)
		}

		userID, err := extractUserID(ctx, secret)
		if err != nil {
			return nil, err
		}

		ctx = context.WithValue(ctx, UserIDKey, userID)
		return handler(ctx, req)
	}
}

func StreamAuthInterceptor(secret string) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if publicMethods[info.FullMethod] || strings.HasPrefix(info.FullMethod, "/grpc.reflection.") || strings.HasPrefix(info.FullMethod, "/grpc.health.") {
			return handler(srv, ss)
		}

		userID, err := extractUserID(ss.Context(), secret)
		if err != nil {
			return err
		}

		wrapped := &wrappedStream{
			ServerStream: ss,
			ctx:          context.WithValue(ss.Context(), UserIDKey, userID),
		}
		return handler(srv, wrapped)
	}
}

func extractUserID(ctx context.Context, secret string) (string, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing metadata")
	}

	authHeader := md.Get("authorization")
	if len(authHeader) == 0 {
		return "", status.Error(codes.Unauthenticated, "missing authorization header")
	}

	tokenStr := strings.TrimPrefix(authHeader[0], "Bearer ")
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, status.Error(codes.Unauthenticated, "invalid signing method")
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", status.Error(codes.Unauthenticated, "invalid token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", status.Error(codes.Unauthenticated, "invalid token claims")
	}

	userID, ok := claims["sub"].(string)
	if !ok {
		return "", status.Error(codes.Unauthenticated, "missing user id in token")
	}

	return userID, nil
}

func UserIDFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(UserIDKey).(string); ok {
		return v
	}
	return ""
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context {
	return w.ctx
}
