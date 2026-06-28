package db

import (
	"context"

	gendb "github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"
	"go.uber.org/fx"
)

type minIOClients struct {
	fx.Out

	Client *minio.Client `name:"minio"`
	Signer *minio.Client `name:"minioSigner"`
}

func providePostgres(lc fx.Lifecycle, ctx context.Context, cfg config.DatabaseConfig) (*pgxpool.Pool, error) {
	pool, err := NewPostgresPool(ctx, cfg)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { pool.Close(); return nil }})
	return pool, nil
}

func provideRedis(lc fx.Lifecycle, ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	client, err := NewRedisClient(ctx, cfg)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { return client.Close() }})
	return client, nil
}

func provideScylla(lc fx.Lifecycle, cfg config.ScyllaDBConfig) (*gocql.Session, error) {
	session, err := NewScyllaSession(cfg)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { session.Close(); return nil }})
	return session, nil
}

func provideNATS(lc fx.Lifecycle, cfg config.NATSConfig) (*nats.Conn, error) {
	conn, err := NewNATSClient(cfg)
	if err != nil {
		return nil, err
	}
	lc.Append(fx.Hook{OnStop: func(context.Context) error { conn.Close(); return nil }})
	return conn, nil
}

func provideMinIO(ctx context.Context, cfg config.MinIOConfig) (minIOClients, error) {
	client, err := NewMinIOClient(ctx, cfg)
	if err != nil {
		return minIOClients{}, err
	}
	signer, err := NewMinIOPublicSigner(cfg)
	if err != nil {
		return minIOClients{}, err
	}
	return minIOClients{Client: client, Signer: signer}, nil
}

func provideQueries(pool *pgxpool.Pool) *gendb.Queries {
	return gendb.New(pool)
}

var Module = fx.Module("db",
	fx.Provide(
		providePostgres,
		provideRedis,
		provideScylla,
		provideNATS,
		provideMinIO,
		provideQueries,
	),
)
