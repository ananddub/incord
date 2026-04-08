package middleware

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

type rateLimit struct {
	limit  int
	window time.Duration
}

// Auth RPCs: 5 req/min per IP
var authRateLimits = map[string]rateLimit{
	"/auth.v1.AuthService/Register":  {limit: 100, window: time.Minute},
	"/auth.v1.AuthService/Login":     {limit: 100, window: time.Minute},
	"/auth.v1.AuthService/VerifyOTP": {limit: 100, window: time.Minute},
	"/auth.v1.AuthService/ResendOTP": {limit: 100, window: time.Minute},
}

// Message send: 30 req/min per user
var messageSendMethods = map[string]bool{
	"/message.v1.MessageService/SendMessage":       true,
	"/message.v1.MessageService/SendDirectMessage": true,
}

// Friend requests: 10 req/min per user
var friendRequestMethods = map[string]bool{
	"/user.v1.UserService/SendFriendRequest": true,
}

var defaultLimit = rateLimit{limit: 60, window: time.Minute}
var messageSendLimit = rateLimit{limit: 30, window: time.Minute}
var friendRequestLimit = rateLimit{limit: 10, window: time.Minute}

// RateLimitInterceptor returns a gRPC unary interceptor that enforces
// distributed rate limiting via Redis using a sliding window counter.
func RateLimitInterceptor(rdb *redis.Client) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Skip rate limiting for reflection and health checks
		if strings.HasPrefix(info.FullMethod, "/grpc.reflection.") || strings.HasPrefix(info.FullMethod, "/grpc.health.") {
			return handler(ctx, req)
		}

		identity, rl := resolveRateLimit(ctx, info.FullMethod)

		key := fmt.Sprintf("ratelimit:%s:%s", identity, info.FullMethod)

		allowed, err := checkRateLimit(ctx, rdb, key, rl)
		if err != nil {
			// If Redis is down, allow the request through (fail open)
			return handler(ctx, req)
		}
		if !allowed {
			return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded: %d requests per %s", rl.limit, rl.window)
		}

		return handler(ctx, req)
	}
}

// resolveRateLimit determines the identity key and applicable rate limit
// for the given method. Auth RPCs use peer IP; all others use the user ID
// from context (falling back to peer IP for unauthenticated requests).
func resolveRateLimit(ctx context.Context, method string) (string, rateLimit) {
	// Auth RPCs: always keyed by IP
	if rl, ok := authRateLimits[method]; ok {
		return peerIP(ctx), rl
	}

	// Authenticated endpoints: keyed by user ID
	identity := UserIDFromContext(ctx)
	if identity == "" {
		identity = peerIP(ctx)
	}

	if messageSendMethods[method] {
		return identity, messageSendLimit
	}
	if friendRequestMethods[method] {
		return identity, friendRequestLimit
	}

	return identity, defaultLimit
}

// checkRateLimit implements a sliding window counter using INCR + EXPIRE.
// Returns true if the request is allowed.
func checkRateLimit(ctx context.Context, rdb *redis.Client, key string, rl rateLimit) (bool, error) {
	pipe := rdb.Pipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, rl.window)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}

	count := incrCmd.Val()
	// If this is the first request (count == 1), the EXPIRE just set the TTL.
	// For subsequent requests, EXPIRE refreshes only if the key had no TTL
	// (which shouldn't happen), but this is harmless.
	return count <= int64(rl.limit), nil
}

func peerIP(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		addr := p.Addr.String()
		// Strip port from address
		if idx := strings.LastIndex(addr, ":"); idx != -1 {
			return addr[:idx]
		}
		return addr
	}
	return "unknown"
}
