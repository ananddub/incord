package channel

import (
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
	"go.uber.org/fx"
)

func provideService(repo *Repository, pubsub *realtime.LPubSub, client *authz.Client) *Service {
	return NewService(repo, pubsub, client)
}

var Module = fx.Module("channel",
	fx.Provide(
		NewRepository,
		provideService,
		NewHandler,
		NewDMResolver,
		NewGuildResolver,
		NewDMChannelMembersResolver,
	),
)
