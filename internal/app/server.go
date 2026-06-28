package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/metrics"
	"go.uber.org/fx"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// Server owns the public API, metrics endpoint, and in-memory gateway loopback.
type Server struct {
	address         string
	grpc            *grpc.Server
	loopback        *bufconn.Listener
	gateway         *grpc.ClientConn
	api             *http.Server
	metrics         *http.Server
	apiListener     net.Listener
	metricsListener net.Listener
}

// NewServer builds all transports without binding network ports.
func NewServer(
	ctx context.Context,
	cfg *config.Config,
	grpcServer *grpc.Server,
	metricsHandler *metrics.Handler,
) (*Server, error) {
	loopback := bufconn.Listen(1 << 20)
	gateway, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return loopback.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create gateway loopback client: %w", err)
	}

	gatewayMux, err := NewGatewayMux(ctx, gateway)
	if err != nil {
		_ = gateway.Close()
		_ = loopback.Close()
		return nil, fmt.Errorf("build REST gateway: %w", err)
	}

	publicHTTP := NewPublicHTTPHandler(gatewayMux)
	dispatcher := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsGRPCRequest(r) {
			grpcServer.ServeHTTP(w, r)
			return
		}
		publicHTTP.ServeHTTP(w, r)
	})

	return &Server{
		address:  fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.GRPCPort),
		grpc:     grpcServer,
		loopback: loopback,
		gateway:  gateway,
		api: &http.Server{
			Handler: h2c.NewHandler(dispatcher, &http2.Server{}),
		},
		metrics: &http.Server{
			Addr:    ":9100",
			Handler: metricsHandler,
		},
	}, nil
}

// Start binds ports and begins serving. Fx calls it during application start.
func (s *Server) Start(context.Context) error {
	apiListener, err := net.Listen("tcp", s.address)
	if err != nil {
		_ = s.gateway.Close()
		_ = s.loopback.Close()
		return fmt.Errorf("listen on %s: %w", s.address, err)
	}
	metricsListener, err := net.Listen("tcp", s.metrics.Addr)
	if err != nil {
		_ = apiListener.Close()
		_ = s.gateway.Close()
		_ = s.loopback.Close()
		return fmt.Errorf("listen on %s: %w", s.metrics.Addr, err)
	}
	s.apiListener = apiListener
	s.metricsListener = metricsListener

	go func() {
		if err := s.grpc.Serve(s.loopback); err != nil {
			logger.Log.Debug().Err(err).Msg("bufconn gRPC server stopped")
		}
	}()
	go serveHTTP(s.api, s.apiListener, "gRPC + REST")
	go serveHTTP(s.metrics, s.metricsListener, "metrics")

	logger.Log.Info().Str("addr", s.address).Msg("gRPC + REST server listening")
	logger.Log.Info().Str("addr", s.metrics.Addr).Msg("metrics server listening")
	return nil
}

// Stop gracefully closes every transport owned by the server.
func (s *Server) Stop(ctx context.Context) error {
	logger.Log.Info().Msg("shutting down server")
	metricsErr := s.metrics.Shutdown(ctx)
	apiErr := s.api.Shutdown(ctx)
	s.grpc.Stop()
	_ = s.loopback.Close()
	gatewayErr := s.gateway.Close()
	return errors.Join(apiErr, metricsErr, gatewayErr)
}

func registerServerLifecycle(lifecycle fx.Lifecycle, server *Server) {
	lifecycle.Append(fx.Hook{
		OnStart: server.Start,
		OnStop:  server.Stop,
	})
}

func serveHTTP(server *http.Server, listener net.Listener, name string) {
	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		logger.Log.Error().Err(err).Str("server", name).Msg("HTTP server stopped")
	}
}
