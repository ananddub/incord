package logger

import "go.uber.org/fx"

var Module = fx.Module("logger",
	fx.Invoke(func() { Init("debug") }),
)
