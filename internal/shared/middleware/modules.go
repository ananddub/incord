package middleware

import (
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
	"google.golang.org/grpc"
)

// GRPCInterceptors keeps interceptor ordering explicit while allowing Fx to
// inject every dependency needed to build the chains.
type GRPCInterceptors struct {
	Unary  []grpc.UnaryServerInterceptor
	Stream []grpc.StreamServerInterceptor
}

func NewGRPCInterceptors(redis *redis.Client, jwt config.JWTConfig) GRPCInterceptors {
	return GRPCInterceptors{
		Unary: []grpc.UnaryServerInterceptor{
			LoggingInterceptor(),
			RateLimitInterceptor(redis),
			AuthInterceptor(jwt.Secret),
			ValidationInterceptor(),
		},
		Stream: []grpc.StreamServerInterceptor{
			StreamLoggingInterceptor(),
			StreamAuthInterceptor(jwt.Secret),
		},
	}
}

var Module = fx.Module("middleware", fx.Provide(NewGRPCInterceptors))
