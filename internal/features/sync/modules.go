package sync

import (
	"github.com/ananddub/ndiscord_backend/internal/features/presence"
	"go.uber.org/fx"
)

func wireDependencies(handler *Handler, presence *presence.Service) {
	handler.SetPresenceReader(presence)
}

var Module = fx.Module("sync",
	fx.Provide(NewHandler),
	fx.Invoke(wireDependencies),
)
