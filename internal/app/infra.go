package app

import (
	"context"

	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/nats-io/nats.go"
	"github.com/redis/go-redis/v9"

	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

// Infra holds all infrastructure connections.
type Infra struct {
	Pool        *pgxpool.Pool
	Redis       *redis.Client
	Scylla      *gocql.Session
	MinIO       *minio.Client
	MinIOSigner *minio.Client
	LPubSub     *realtime.LPubSub
	NATS        *nats.Conn
	Authz       *authz.Client
}

// NewInfra connects to all databases and services.
func NewInfra(ctx context.Context, cfg *config.Config) (*Infra, error) {
	pool, err := db.NewPostgresPool(ctx, cfg.Database)
	if err != nil {
		return nil, err
	}

	rdb, err := db.NewRedisClient(ctx, cfg.Redis)
	if err != nil {
		pool.Close()
		return nil, err
	}

	scylla, err := db.NewScyllaSession(cfg.ScyllaDB)
	if err != nil {
		pool.Close()
		rdb.Close()
		return nil, err
	}

	minioClient, err := db.NewMinIOClient(ctx, cfg.MinIO)
	if err != nil {
		pool.Close()
		rdb.Close()
		scylla.Close()
		return nil, err
	}

	minioSigner, err := db.NewMinIOPublicSigner(cfg.MinIO)
	if err != nil {
		pool.Close()
		rdb.Close()
		scylla.Close()
		return nil, err
	}
	nats, err := db.NewNATSClient(cfg.NATS)
	if err != nil {
		pool.Close()
		rdb.Close()
		scylla.Close()
		return nil, err
	}
	natsHub, err := realtime.NewLPubSub(nats, rdb)
	if err != nil {
		pool.Close()
		rdb.Close()
		scylla.Close()
		return nil, err
	}

	authzClient := authz.NewClient(pool)

	return &Infra{
		Pool:        pool,
		Redis:       rdb,
		Scylla:      scylla,
		MinIO:       minioClient,
		MinIOSigner: minioSigner,
		LPubSub:     natsHub,
		NATS:        nats,
		Authz:       authzClient,
	}, nil
}

func (i *Infra) Close() {
	i.Pool.Close()
	i.Redis.Close()
	i.Scylla.Close()
	i.NATS.Close()
}
