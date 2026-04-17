package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/ananddub/ndiscord_backend/internal/app"
	"github.com/ananddub/ndiscord_backend/internal/features/voice"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

func main() {
	cfg := config.Load()
	logger.Init("debug")
	log := logger.Log
	ctx := context.Background()

	log.Info().Msg("starting ndiscord server")

	// Connect infrastructure (before metrics so webhook handler can use NATS)
	infra, err := app.NewInfra(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialize infrastructure")
	}
	defer infra.Close()

	// Wire all feature handlers
	handlers := app.NewHandlers(infra, cfg)

	// LiveKit webhook receiver — needs voiceSvc for Redis state sync.
	webhookH := voice.NewWebhookHandler(cfg.LiveKit, infra.NATS, handlers.VoiceSvc)
	go app.StartMetricsServer(map[string]http.Handler{
		"/livekit/webhook": webhookH,
	})

	// Create gRPC server
	srv := app.NewGRPCServer(handlers, cfg.JWT.Secret, infra.Redis)

	// Listen
	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", addr).Msg("failed to listen")
	}

	go func() {
		log.Info().Str("addr", addr).Msg("gRPC server listening")
		if err := srv.Serve(lis); err != nil {
			log.Fatal().Err(err).Msg("failed to serve")
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down server")
	srv.GracefulStop()
}
