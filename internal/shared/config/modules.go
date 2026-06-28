package config

import "go.uber.org/fx"

var Module = fx.Module("config",
	fx.Provide(
		Load,
		func(cfg *Config) ServerConfig { return cfg.Server },
		func(cfg *Config) DatabaseConfig { return cfg.Database },
		func(cfg *Config) RedisConfig { return cfg.Redis },
		func(cfg *Config) ScyllaDBConfig { return cfg.ScyllaDB },
		func(cfg *Config) MinIOConfig { return cfg.MinIO },
		func(cfg *Config) JWTConfig { return cfg.JWT },
		func(cfg *Config) VoiceConfig { return cfg.Voice },
		func(cfg *Config) LiveKitConfig { return cfg.LiveKit },
		func(cfg *Config) SMTPConfig { return cfg.SMTP },
		func(cfg *Config) NATSConfig { return cfg.NATS },
	),
)
