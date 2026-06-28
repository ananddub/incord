package message

import (
	"github.com/ananddub/ndiscord_backend/internal/features/channel"
	"github.com/ananddub/ndiscord_backend/internal/features/media"
	"github.com/ananddub/ndiscord_backend/internal/features/user"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
)

func provideService(repo *Repository, redis *redis.Client, pubsub *realtime.LPubSub, client *authz.Client) *Service {
	return NewService(repo, redis, pubsub, client)
}

func wireDependencies(
	service *Service,
	handler *Handler,
	dmResolver *channel.DMResolver,
	guildResolver *channel.GuildResolver,
	dmChannelLister *channel.DMChannelMembersResolver,
	blockChecker *user.BlockChecker,
	mediaService *media.Service,
) {
	service.SetDMResolver(dmResolver)
	service.SetBlockChecker(blockChecker)
	service.SetDMChannelLister(dmChannelLister)
	service.SetMediaResolver(mediaService)
	handler.SetGuildResolver(guildResolver)
}

var Module = fx.Module("message",
	fx.Provide(NewRepository, provideService, NewHandler),
	fx.Invoke(wireDependencies),
)
