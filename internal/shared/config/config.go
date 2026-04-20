package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	Redis    RedisConfig
	ScyllaDB ScyllaDBConfig
	MinIO    MinIOConfig
	JWT      JWTConfig
	Voice    VoiceConfig
	LiveKit  LiveKitConfig
	SMTP     SMTPConfig
	NATS     NATSConfig
	// InviteBaseURL is the public base URL under which invite codes are
	// reachable, e.g. "https://ndiscord.app/invite". Final URL = base + "/" + code.
	InviteBaseURL string
}

type ServerConfig struct {
	GRPCPort int
	Host     string
}

type DatabaseConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string
}

type RedisConfig struct {
	Addr     string
	Password string
	DB       int
}

type ScyllaDBConfig struct {
	Hosts    []string
	Keyspace string
}

type LiveKitConfig struct {
	URL       string
	HTTPURL   string
	APIKey    string
	APISecret string
}

type MinIOConfig struct {
	Endpoint       string
	PublicEndpoint string
	AccessKey      string
	SecretKey      string
	Bucket         string
	UseSSL         bool
}

type JWTConfig struct {
	Secret          string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
}

type VoiceConfig struct {
	UDPHost string
	UDPPort int
}

type SMTPConfig struct {
	Host string
	Port int
	From string
}

type NATSConfig struct {
	URL string
}

func Load() *Config {
	return &Config{
		Server: ServerConfig{
			GRPCPort: envInt("GRPC_PORT", 50051),
			Host:     env("HOST", "0.0.0.0"),
		},
		Database: DatabaseConfig{
			Host:     env("DB_HOST", "localhost"),
			Port:     envInt("DB_PORT", 5432),
			User:     env("DB_USER", "ndiscord"),
			Password: env("DB_PASSWORD", "ndiscord"),
			DBName:   env("DB_NAME", "ndiscord"),
			SSLMode:  env("DB_SSLMODE", "disable"),
		},
		Redis: RedisConfig{
			Addr:     env("REDIS_ADDR", "localhost:6379"),
			Password: env("REDIS_PASSWORD", ""),
			DB:       envInt("REDIS_DB", 0),
		},
		ScyllaDB: ScyllaDBConfig{
			Hosts:    []string{env("SCYLLA_HOST", "localhost:9042")},
			Keyspace: env("SCYLLA_KEYSPACE", "ndiscord"),
		},
		MinIO: MinIOConfig{
			Endpoint:       env("MINIO_ENDPOINT", "localhost:9000"),
			PublicEndpoint: env("MINIO_PUBLIC_ENDPOINT", env("MINIO_ENDPOINT", "localhost:9000")),
			AccessKey:      env("MINIO_ACCESS_KEY", "ndiscord"),
			SecretKey:      env("MINIO_SECRET_KEY", "ndiscord123"),
			Bucket:         env("MINIO_BUCKET", "ndiscord"),
			UseSSL:         envBool("MINIO_USE_SSL", false),
		},
		JWT: JWTConfig{
			Secret:          env("JWT_SECRET", "change-me-in-production"),
			AccessTokenTTL:  envDuration("JWT_ACCESS_TTL", 7*24*time.Hour),
			RefreshTokenTTL: envDuration("JWT_REFRESH_TTL", 7*24*time.Hour),
		},
		Voice: VoiceConfig{
			UDPHost: env("VOICE_UDP_HOST", "0.0.0.0"),
			UDPPort: envInt("VOICE_UDP_PORT", 50052),
		},
		LiveKit: LiveKitConfig{
			URL:       env("LIVEKIT_URL", "ws://localhost:7880"),
			HTTPURL:   env("LIVEKIT_HTTP_URL", "http://localhost:7880"),
			APIKey:    env("LIVEKIT_API_KEY", "devkey"),
			APISecret: env("LIVEKIT_API_SECRET", "secret"),
		},
		SMTP: SMTPConfig{
			Host: env("SMTP_HOST", "localhost"),
			Port: envInt("SMTP_PORT", 1025),
			From: env("SMTP_FROM", "noreply@ndiscord.local"),
		},
		NATS: NATSConfig{
			URL: env("NATS_URL", "nats://localhost:4222"),
		},
		InviteBaseURL: env("INVITE_BASE_URL", "https://ndiscord.app/invite"),
	}
}

func (d DatabaseConfig) DSN() string {
	return "postgres://" + d.User + ":" + d.Password + "@" + d.Host + ":" + strconv.Itoa(d.Port) + "/" + d.DBName + "?sslmode=" + d.SSLMode
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
