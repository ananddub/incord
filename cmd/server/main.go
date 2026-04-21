package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ananddub/ndiscord_backend/internal/app"
	"github.com/ananddub/ndiscord_backend/internal/features/voice"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
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

	// REST gateway — same-process handlers, Discord-style paths under /v1/*.
	// Auth middleware on the HTTP side would go here; for now REST inherits
	// whatever validation the gRPC handler enforces internally.
	gwMux, err := app.NewGatewayMux(ctx, handlers)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to build REST gateway mux")
	}
	publicHTTP := app.NewPublicHTTPHandler(gwMux, cfg.JWT.Secret, infra.Redis)

	// Single-port dispatch: HTTP/2 clients sending `application/grpc` go to
	// the gRPC server; everything else (HTTP/1.1 JSON, browser SSE, Swagger
	// UI) goes to the REST mux. h2c lets us speak HTTP/2 plaintext so no
	// TLS is required locally.
	dispatcher := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if app.IsGRPCRequest(r) {
			srv.ServeHTTP(w, r)
			return
		}
		publicHTTP.ServeHTTP(w, r)
	})

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.GRPCPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal().Err(err).Str("addr", addr).Msg("failed to listen")
	}

	httpServer := &http.Server{
		Handler: h2c.NewHandler(dispatcher, &http2.Server{}),
	}

	go func() {
		log.Info().Str("addr", addr).Msg("gRPC + REST server listening")
		if err := httpServer.Serve(lis); err != nil && err != http.ErrServerClosed {
			log.Fatal().Err(err).Msg("failed to serve")
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down server")
	shutdownCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	httpServer.Shutdown(shutdownCtx)
	srv.GracefulStop()
}
