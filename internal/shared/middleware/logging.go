package middleware

import (
	"context"
	"time"

	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

func LoggingInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		duration := time.Since(start)

		st, _ := status.FromError(err)
		metrics.GrpcRequestDuration.WithLabelValues(info.FullMethod, st.Code().String()).Observe(duration.Seconds())

		logger.Log.Info().
			Str("method", info.FullMethod).
			Str("code", st.Code().String()).
			Dur("duration", duration).
			Str("user_id", UserIDFromContext(ctx)).
			Msg("grpc request")

		return resp, err
	}
}

func StreamLoggingInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()
		err := handler(srv, ss)
		duration := time.Since(start)

		st, _ := status.FromError(err)
		logger.Log.Info().
			Str("method", info.FullMethod).
			Str("code", st.Code().String()).
			Dur("duration", duration).
			Msg("grpc stream")

		return err
	}
}
