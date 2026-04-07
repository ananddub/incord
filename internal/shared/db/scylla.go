package db

import (
	"fmt"
	"time"

	"github.com/gocql/gocql"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

func NewScyllaSession(cfg config.ScyllaDBConfig) (*gocql.Session, error) {
	cluster := gocql.NewCluster(cfg.Hosts...)
	cluster.Keyspace = cfg.Keyspace
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create scylla session: %w", err)
	}

	logger.Log.Info().Strs("hosts", cfg.Hosts).Str("keyspace", cfg.Keyspace).Msg("connected to scylladb")
	return session, nil
}
