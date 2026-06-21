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
	"github.com/rs/zerolog"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func main() {
	cfg := config.Load()
	logger.Init(zerolog.LevelDebugValue)
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
	webhookH := voice.NewWebhookHandler(cfg.LiveKit, infra.LPubSub, handlers.VoiceSvc)
	go app.StartMetricsServer(map[string]http.Handler{
		"/livekit/webhook": webhookH,
	})

	// Create gRPC server
	srv := app.NewGRPCServer(handlers, cfg.JWT.Secret, infra.Redis)

	// In-memory loopback for the REST gateway. REST requests flow:
	//   HTTP /v1/... → grpc-gateway mux → bufconn → grpc.Server → interceptors → handler
	// This is what gives REST calls the same auth / rate-limit / validation
	// chain as native gRPC, and critically it's the only path that supports
	// server-streaming RPCs (the in-process HandlerServer variant does not).
	bufLis := bufconn.Listen(1 << 20) // 1 MiB pipe buffer
	go func() {
		if err := srv.Serve(bufLis); err != nil {
			log.Error().Err(err).Msg("bufconn gRPC serve stopped")
		}
	}()
	gwConn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return bufLis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to dial bufconn loopback")
	}
	defer gwConn.Close()

	gwMux, err := app.NewGatewayMux(ctx, gwConn)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to build REST gateway mux")
	}
	publicHTTP := app.NewPublicHTTPHandler(gwMux)

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
	srv.Stop()
}
