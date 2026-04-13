package app

import (
	gendb "github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/features/auth"
	"github.com/ananddub/ndiscord_backend/internal/features/channel"
	"github.com/ananddub/ndiscord_backend/internal/features/guild"
	"github.com/ananddub/ndiscord_backend/internal/features/media"
	"github.com/ananddub/ndiscord_backend/internal/features/message"
	"github.com/ananddub/ndiscord_backend/internal/features/presence"
	"github.com/ananddub/ndiscord_backend/internal/features/stream"
	"github.com/ananddub/ndiscord_backend/internal/features/sync"
	"github.com/ananddub/ndiscord_backend/internal/features/user"
	"github.com/ananddub/ndiscord_backend/internal/features/voice"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/mail"
)

// Handlers holds all gRPC service handlers.
type Handlers struct {
	Auth     *auth.Handler
	User     *user.Handler
	Guild    *guild.Handler
	Channel  *channel.Handler
	Message  *message.Handler
	Stream   *stream.Handler
	Sync     *sync.Handler
	Presence *presence.Handler
	Media    *media.Handler
	Voice    *voice.Handler
}

// NewHandlers wires up all feature services and returns their handlers.
func NewHandlers(infra *Infra, cfg *config.Config) *Handlers {
	queries := gendb.New(infra.Pool)

	// Auth
	authRepo := auth.NewRepository(infra.Pool, infra.Redis)
	mailer := mail.NewSender(cfg.SMTP)
	authSvc := auth.NewService(authRepo, cfg.JWT, mailer)
	authHandler := auth.NewHandler(authSvc)

	// User
	userRepo := user.NewRepository(infra.Pool, infra.Redis)
	userSvc := user.NewService(userRepo)
	userHandler := user.NewHandler(userSvc)
	userHandler.SetBlockChecker(user.NewBlockChecker(userRepo))

	// Guild
	guildRepo := guild.NewRepository(infra.Pool, infra.Redis)
	guildSvc := guild.NewService(guildRepo, infra.NATS, infra.Authz)
	guildSvc.SetStorage(infra.MinIO, infra.MinIOSigner, cfg.MinIO.Bucket)
	guildHandler := guild.NewHandler(guildSvc)

	// Channel
	channelRepo := channel.NewRepository(infra.Pool)
	channelSvc := channel.NewService(channelRepo, infra.NATS, infra.Authz)
	channelHandler := channel.NewHandler(channelSvc)

	// Message
	messageRepo := message.NewRepository(infra.Scylla)
	messageSvc := message.NewService(messageRepo, infra.Redis, infra.NATS, infra.Authz)
	messageSvc.SetDMResolver(channel.NewDMResolver(channelSvc))
	messageSvc.SetBlockChecker(user.NewBlockChecker(userRepo))
	messageSvc.SetDMChannelLister(channel.NewDMChannelMembersResolver(channelRepo))
	messageHandler := message.NewHandler(messageSvc)
	messageHandler.SetGuildResolver(channel.NewGuildResolver(channelSvc))

	// Stream + Sync
	streamResolver := stream.NewResolver(queries)
	streamHandler := stream.NewHandler(infra.NATS, streamResolver)
	syncHandler := sync.NewHandler(queries, messageRepo)

	// Presence
	presenceSvc := presence.NewService(infra.Redis)
	presenceHandler := presence.NewHandler(presenceSvc)

	// Wire presence updater for logout → offline
	authHandler.SetPresenceUpdater(presenceSvc)

	// Media
	mediaRepo := media.NewRepository(queries)
	mediaSvc := media.NewService(mediaRepo, infra.MinIO, cfg.MinIO)
	mediaHandler := media.NewHandler(mediaSvc)

	// Voice
	voiceSvc := voice.NewService(infra.Redis, cfg.Voice, infra.NATS, infra.Authz)
	voiceHandler := voice.NewHandler(voiceSvc)

	return &Handlers{
		Auth:     authHandler,
		User:     userHandler,
		Guild:    guildHandler,
		Channel:  channelHandler,
		Message:  messageHandler,
		Stream:   streamHandler,
		Sync:     syncHandler,
		Presence: presenceHandler,
		Media:    mediaHandler,
		Voice:    voiceHandler,
	}
}
