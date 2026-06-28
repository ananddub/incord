package auth

import (
	"github.com/ananddub/ndiscord_backend/internal/features/presence"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/mail"
	"go.uber.org/fx"
)

func provideService(repo *Repository, cfg config.JWTConfig, sender *mail.Sender) *Service {
	return NewService(repo, cfg, sender)
}

func wireDependencies(handler *Handler, presence *presence.Service) {
	handler.SetPresenceUpdater(presence)
}

var Module = fx.Module("auth",
	fx.Provide(NewRepository, provideService, NewHandler),
	fx.Invoke(wireDependencies),
)
