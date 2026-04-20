package app

import (
	"context"

	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"
	"github.com/redis/go-redis/v9"

	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

// Infra holds all infrastructure connections.
type Infra struct {
	Pool         *pgxpool.Pool
	Redis        *redis.Client
	Scylla       *gocql.Session
	MinIO        *minio.Client
	MinIOSigner  *minio.Client
	NATS         *realtime.Hub
	Authz        *authz.Client
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

	natsHub, err := realtime.NewHub(cfg.NATS)
	if err != nil {
		pool.Close()
		rdb.Close()
		scylla.Close()
		return nil, err
	}

	// Authz is a thin Postgres wrapper now — share the same pool that
	// feature services already use, no separate dial / health gate
	// / env block needed.
	authzClient := authz.NewClient(pool)

	return &Infra{
		Pool:        pool,
		Redis:       rdb,
		Scylla:      scylla,
		MinIO:       minioClient,
		MinIOSigner: minioSigner,
		NATS:        natsHub,
		Authz:       authzClient,
	}, nil
}

func (i *Infra) Close() {
	i.Pool.Close()
	i.Redis.Close()
	i.Scylla.Close()
	i.NATS.Close()
}
