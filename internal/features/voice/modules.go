package voice

import (
	"github.com/ananddub/ndiscord_backend/internal/features/channel"
	"github.com/ananddub/ndiscord_backend/internal/features/user"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/metrics"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
)

func provideService(cfg config.LiveKitConfig, pubsub *realtime.LPubSub, redis *redis.Client, client *authz.Client) *Service {
	return NewService(cfg, pubsub, redis, client)
}

func provideMetricsRoute(handler *WebhookHandler) metrics.Route {
	return metrics.Route{Pattern: "/livekit/webhook", Handler: handler}
}

func wireDependencies(service *Service, dmResolver *channel.DMResolver, profile *user.Service) {
	service.SetDMResolver(dmResolver)
	service.SetProfileResolver(profile)
}

var Module = fx.Module("voice",
	fx.Provide(
		provideService,
		NewHandler,
		NewWebhookHandler,
		NewVoiceSnapshot,
		fx.Annotate(provideMetricsRoute, fx.ResultTags(`group:"metricsRoutes"`)),
	),
	fx.Invoke(wireDependencies),
)
