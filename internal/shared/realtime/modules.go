package realtime

import "go.uber.org/fx"

var Module = fx.Module("realtime", fx.Provide(NewLPubSub))
