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
	VoiceSvc *voice.Service
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
	userSvc := user.NewService(userRepo, infra.LPubSub)
	userSvc.SetStorage(infra.MinIO, infra.MinIOSigner, cfg.MinIO.Bucket)
	userHandler := user.NewHandler(userSvc)
	userHandler.SetBlockChecker(user.NewBlockChecker(userRepo))

	// Guild
	guildRepo := guild.NewRepository(infra.Pool, infra.Redis)
	guildSvc := guild.NewService(guildRepo, infra.LPubSub, infra.Authz)
	guildSvc.SetStorage(infra.MinIO, infra.MinIOSigner, cfg.MinIO.Bucket)
	guildSvc.SetInviteBaseURL(cfg.InviteBaseURL)
	guildHandler := guild.NewHandler(guildSvc)

	// Channel
	channelRepo := channel.NewRepository(infra.Pool)
	channelSvc := channel.NewService(channelRepo, infra.LPubSub, infra.Authz)
	channelHandler := channel.NewHandler(channelSvc)

	// Wire the DM-opener into user service so accepting a friend request
	// automatically creates (or reuses) the DM channel.
	userSvc.SetDMOpener(channel.NewDMResolver(channelSvc))

	// Message
	messageRepo := message.NewRepository(infra.Scylla)
	messageSvc := message.NewService(messageRepo, infra.Redis, infra.LPubSub, infra.Authz)
	messageSvc.SetDMResolver(channel.NewDMResolver(channelSvc))
	messageSvc.SetBlockChecker(user.NewBlockChecker(userRepo))
	messageSvc.SetDMChannelLister(channel.NewDMChannelMembersResolver(channelRepo))
	messageHandler := message.NewHandler(messageSvc)
	messageHandler.SetGuildResolver(channel.NewGuildResolver(channelSvc))

	// Stream + Sync
	streamResolver := stream.NewResolver(queries)
	streamHandler := stream.NewHandler(infra.LPubSub, streamResolver)
	// Per-channel fan-out filtering — drops events for channels the
	// subscriber can't view (private-channel privacy).
	if infra.Authz != nil {
		streamHandler.SetChannelViewer(infra.Authz)
	}
	syncHandler := sync.NewHandler(queries, messageRepo)

	// Presence
	presenceSvc := presence.NewService(infra.Redis, infra.LPubSub)
	presenceSvc.SetUserResolver(userSvc)
	presenceHandler := presence.NewHandler(presenceSvc)

	// Wire presence updater for logout → offline
	authHandler.SetPresenceUpdater(presenceSvc)

	// Wire the presence lifecycle to StreamFriendActivity so friends see
	// accurate online/offline state based on whether a user has the
	// activity stream open.
	streamHandler.SetPresenceController(presenceSvc)

	// Let user-service stamp live presence on friend/profile enrichment
	// events so a recipient sees the actor's actual current state, not
	// the stale DB intent.
	userSvc.SetPresenceReader(presenceSvc)

	// Same reason for sync: when a client app-opens and pulls SyncUsers,
	// an offline friend should show as "offline" regardless of what
	// status they had set before disconnecting.
	syncHandler.SetPresenceReader(presenceSvc)

	// Media
	mediaRepo := media.NewRepository(queries)
	mediaSvc := media.NewService(mediaRepo, infra.MinIO, infra.MinIOSigner, cfg.MinIO)
	mediaHandler := media.NewHandler(mediaSvc)

	// Wire the media resolver now that both services exist so SendMessage
	// can attach uploaded files to messages.
	messageSvc.SetMediaResolver(mediaSvc)

	// Voice (LiveKit SFU)
	voiceSvc := voice.NewService(cfg.LiveKit, infra.LPubSub, infra.Redis, infra.Authz)
	voiceSvc.SetDMResolver(channel.NewDMResolver(channelSvc))
	voiceSvc.SetProfileResolver(userSvc)
	voiceHandler := voice.NewHandler(voiceSvc)
	streamHandler.SetVoiceSnapshotProvider(voice.NewVoiceSnapshot(voiceSvc))

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
		VoiceSvc: voiceSvc,
	}
}
