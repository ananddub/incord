package app

import (
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
	"go.uber.org/fx"
)

// Handlers contains every gRPC service implementation registered by Server.
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

type handlerParams struct {
	fx.In

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

// NewHandlers collects handlers created by the individual feature modules.
func NewHandlers(p handlerParams) *Handlers {
	return &Handlers{
		Auth:     p.Auth,
		User:     p.User,
		Guild:    p.Guild,
		Channel:  p.Channel,
		Message:  p.Message,
		Stream:   p.Stream,
		Sync:     p.Sync,
		Presence: p.Presence,
		Media:    p.Media,
		Voice:    p.Voice,
	}
}
