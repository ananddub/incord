package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	gatewayv1 "github.com/ananddub/ndiscord_backend/gen/gateway/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	mediav1 "github.com/ananddub/ndiscord_backend/gen/media/v1"
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	gendb "github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/features/auth"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/mail"
	"github.com/ananddub/ndiscord_backend/internal/features/channel"
	"github.com/ananddub/ndiscord_backend/internal/features/gateway"
	"github.com/ananddub/ndiscord_backend/internal/features/guild"
	"github.com/ananddub/ndiscord_backend/internal/features/media"
	"github.com/ananddub/ndiscord_backend/internal/features/message"
	"github.com/ananddub/ndiscord_backend/internal/features/presence"
	"github.com/ananddub/ndiscord_backend/internal/features/user"
	"github.com/ananddub/ndiscord_backend/internal/features/voice"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/event"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	cfg := config.Load()
	logger.Init("debug")
	log := logger.Log

	ctx := context.Background()

	log.Info().Msg("starting ndiscord server")

	// Prometheus metrics HTTP endpoint
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("ok"))
		})
		log.Info().Msg("metrics server listening on :9100/metrics")
		if err := http.ListenAndServe(":9100", mux); err != nil {
			log.Error().Err(err).Msg("metrics server failed")
		}
	}()

	// Connect to databases
	pool, err := db.NewPostgresPool(ctx, cfg.Database)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to timescaledb")
	}
	defer pool.Close()

	rdb, err := db.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to redis")
	}
	defer rdb.Close()

	scylla, err := db.NewScyllaSession(cfg.ScyllaDB)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to scylladb")
	}
	defer scylla.Close()

	minioClient, err := db.NewMinIOClient(ctx, cfg.MinIO)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to connect to minio")
	}

	producer, err := event.NewProducer(cfg.Redpanda)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create redpanda producer")
	}
	defer producer.Close()

	// Auth
	authRepo := auth.NewRepository(pool, rdb)
	mailer := mail.NewSender(cfg.SMTP)
	authSvc := auth.NewService(authRepo, cfg.JWT, mailer)
	authHandler := auth.NewHandler(authSvc)

	// Gateway + Event Dispatcher (create FIRST so handlers share its subscription managers)
	sessionMgr := gateway.NewSessionManager()
	gatewaySvc := gateway.NewService(sessionMgr, cfg.JWT.Secret)
	gatewayHandler := gateway.NewHandler(gatewaySvc)

	allTopics := []string{
		event.TopicMessageCreate, event.TopicMessageUpdate, event.TopicMessageDelete,
		event.TopicGuildEvents, event.TopicChannelEvents,
		event.TopicPresence, event.TopicTyping, event.TopicVoiceState, event.TopicUserUpdate,
	}
	consumer, err := event.NewConsumer(cfg.Redpanda, "ndiscord-gateway", allTopics)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create redpanda consumer")
	}
	defer consumer.Close()

	// Create OpenFGA authz client (nil-safe: if it fails, services allow all)
	authzClient, err := authz.NewClient(cfg.OpenFGA)
	if err != nil {
		log.Warn().Err(err).Msg("failed to create OpenFGA client, running without authorization")
		authzClient = nil
	}

	// User + Guild repos needed for dispatcher (friend lookup + authz)
	userRepo := user.NewRepository(pool, rdb)
	friendLookup := user.NewFriendLookup(userRepo)

	guildRepo := guild.NewRepository(pool, rdb)
	guildSvc := guild.NewService(guildRepo, producer, authzClient)

	dispatcher := gateway.NewDispatcher(sessionMgr, consumer, friendLookup, authzClient)
	go func() {
		if err := dispatcher.Start(ctx); err != nil {
			log.Error().Err(err).Msg("event dispatcher stopped")
		}
	}()

	// User
	userSvc := user.NewService(userRepo, producer)
	userHandler := user.NewHandler(userSvc, dispatcher.FriendSubs)
	userHandler.SetBlockChecker(user.NewBlockChecker(userRepo))

	// Guild - uses dispatcher.GuildSubs
	guildHandler := guild.NewHandler(guildSvc, dispatcher.GuildSubs)

	// Channel
	channelRepo := channel.NewRepository(pool)
	channelSvc := channel.NewService(channelRepo, producer, authzClient)
	channelHandler := channel.NewHandler(channelSvc)

	// Message - uses dispatcher.MsgSubs and dispatcher.TypingSubs
	messageRepo := message.NewRepository(scylla)
	messageSvc := message.NewService(messageRepo, producer, rdb, authzClient)
	messageSvc.SetDMResolver(channel.NewDMResolver(channelSvc))
	messageSvc.SetBlockChecker(user.NewBlockChecker(userRepo))
	messageHandler := message.NewHandler(messageSvc, dispatcher.MsgSubs, dispatcher.TypingSubs)
	messageHandler.SetGuildResolver(channel.NewGuildResolver(channelSvc))

	// Presence
	presenceSvc := presence.NewService(rdb, producer)
	presenceHandler := presence.NewHandler(presenceSvc)

	// Wire presence updater for logout/disconnect -> offline
	authHandler.SetPresenceUpdater(presenceSvc)
	gatewayHandler.SetPresenceUpdater(presenceSvc)

	// Media
	queries := gendb.New(pool)
	mediaRepo := media.NewRepository(queries)
	mediaSvc := media.NewService(mediaRepo, minioClient, cfg.MinIO)
	mediaHandler := media.NewHandler(mediaSvc)

	// Voice - uses dispatcher.VoiceSubs
	voiceSvc := voice.NewService(rdb, producer, cfg.Voice, authzClient)
	voiceHandler := voice.NewHandler(voiceSvc, dispatcher.VoiceSubs)

	// gRPC server with middleware
	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			middleware.LoggingInterceptor(),
			middleware.RateLimitInterceptor(rdb),
			middleware.AuthInterceptor(cfg.JWT.Secret),
			middleware.ValidationInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			middleware.StreamLoggingInterceptor(),
			middleware.StreamAuthInterceptor(cfg.JWT.Secret),
		),
	)
	reflection.Register(srv)

	// Register all services
	authv1.RegisterAuthServiceServer(srv, authHandler)
	userv1.RegisterUserServiceServer(srv, userHandler)
	guildv1.RegisterGuildServiceServer(srv, guildHandler)
	channelv1.RegisterChannelServiceServer(srv, channelHandler)
	messagev1.RegisterMessageServiceServer(srv, messageHandler)
	gatewayv1.RegisterGatewayServiceServer(srv, gatewayHandler)
	presencev1.RegisterPresenceServiceServer(srv, presenceHandler)
	mediav1.RegisterMediaServiceServer(srv, mediaHandler)
	voicev1.RegisterVoiceServiceServer(srv, voiceHandler)

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

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info().Msg("shutting down server")
	srv.GracefulStop()
}
