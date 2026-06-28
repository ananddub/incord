package authz

import "go.uber.org/fx"

var Module = fx.Module("authz", fx.Provide(NewClient))
