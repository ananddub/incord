package presence

import "go.uber.org/fx"

var Module = fx.Module("presence",
	fx.Provide(NewService, NewHandler),
)
