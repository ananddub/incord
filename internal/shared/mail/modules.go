package mail

import "go.uber.org/fx"

var Module = fx.Module("mail", fx.Provide(NewSender))
