package app

import (
	"context"

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
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	shareddb "github.com/ananddub/ndiscord_backend/internal/shared/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/mail"
	"github.com/ananddub/ndiscord_backend/internal/shared/metrics"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
	"go.uber.org/fx"
)

// Module is the complete dependency graph for the API process.
var Module = fx.Module("app",
	logger.Module,
	config.Module,
	shareddb.Module,
	realtime.Module,
	authz.Module,
	mail.Module,
	metrics.Module,
	middleware.Module,
	auth.Module,
	user.Module,
	guild.Module,
	channel.Module,
	message.Module,
	stream.Module,
	sync.Module,
	presence.Module,
	media.Module,
	voice.Module,
	fx.Provide(
		func() context.Context { return context.Background() },
		NewHandlers,
		NewGRPCServer,
		NewServer,
	),
	fx.Invoke(
		registerServerLifecycle,
	),
)

// New builds the API application. Extra options are primarily useful for tests.
func New(options ...fx.Option) *fx.App {
	return fx.New(append([]fx.Option{Module}, options...)...)
}
