package app

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	mediav1 "github.com/ananddub/ndiscord_backend/gen/media/v1"
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	syncv1 "github.com/ananddub/ndiscord_backend/gen/sync/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"

	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// NewGRPCServer creates a gRPC server with all interceptors and registers all services.
func NewGRPCServer(h *Handlers, jwtSecret string, rdb *redis.Client) *grpc.Server {
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			middleware.LoggingInterceptor(),
			middleware.RateLimitInterceptor(rdb),
			middleware.AuthInterceptor(jwtSecret),
			middleware.ValidationInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			middleware.StreamLoggingInterceptor(),
			middleware.StreamAuthInterceptor(jwtSecret),
		),
	)
	reflection.Register(srv)

	authv1.RegisterAuthServiceServer(srv, h.Auth)
	userv1.RegisterUserServiceServer(srv, h.User)
	guildv1.RegisterGuildServiceServer(srv, h.Guild)
	channelv1.RegisterChannelServiceServer(srv, h.Channel)
	messagev1.RegisterMessageServiceServer(srv, h.Message)
	streamv1.RegisterStreamServiceServer(srv, h.Stream)
	syncv1.RegisterSyncServiceServer(srv, h.Sync)
	presencev1.RegisterPresenceServiceServer(srv, h.Presence)
	mediav1.RegisterMediaServiceServer(srv, h.Media)
	voicev1.RegisterVoiceServiceServer(srv, h.Voice)

	return srv
}

// StartMetricsServer starts the Prometheus /metrics, /health, and LiveKit
// webhook HTTP server. The webhook handler is optional — pass nil to skip.
func StartMetricsServer(extraHandlers map[string]http.Handler) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	for path, h := range extraHandlers {
		mux.Handle(path, h)
		logger.Log.Info().Str("path", path).Msg("registered HTTP handler")
	}
	logger.Log.Info().Msg("metrics server listening on :9100/metrics")
	if err := http.ListenAndServe(":9100", mux); err != nil {
		logger.Log.Error().Err(err).Msg("metrics server failed")
	}
}
