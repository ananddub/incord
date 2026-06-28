package stream

import (
	"github.com/ananddub/ndiscord_backend/internal/features/presence"
	"github.com/ananddub/ndiscord_backend/internal/features/voice"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
	"go.uber.org/fx"
)

func provideHandler(pubsub *realtime.LPubSub, resolver *Resolver) *Handler {
	return NewHandler(pubsub, resolver)
}

func wireDependencies(handler *Handler, presence *presence.Service, authz *authz.Client, snapshot *voice.VoiceSnapshot) {
	handler.SetPresenceController(presence)
	handler.SetChannelViewer(authz)
	handler.SetVoiceSnapshotProvider(snapshot)
}

var Module = fx.Module("stream",
	fx.Provide(NewResolver, provideHandler),
	fx.Invoke(wireDependencies),
)
