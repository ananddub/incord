package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/sqlutil"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	logger.Init("info")
	log := logger.Log
	cfg := config.Load()
	ctx := context.Background()

	log.Info().Msg("=== ndiscord init ===")

	// [1/3] TimescaleDB
	log.Info().Msg("[1/3] Running TimescaleDB migrations...")
	if err := migratePG(ctx, cfg.Database); err != nil {
		log.Fatal().Err(err).Msg("TimescaleDB migration failed")
	}
	log.Info().Msg("  TimescaleDB: done")

	// [2/3] ScyllaDB
	log.Info().Msg("[2/3] Setting up ScyllaDB...")
	if err := migrateScylla(cfg.ScyllaDB); err != nil {
		log.Fatal().Err(err).Msg("ScyllaDB setup failed")
	}
	log.Info().Msg("  ScyllaDB: done")

	log.Info().Msg("=== Init complete! ===")
}

// migratePG reads Goose .sql files from db/timescale/migrations/ and executes their Up sections.
func migratePG(ctx context.Context, cfg config.DatabaseConfig) error {
	var pool *pgxpool.Pool
	var err error
	for range 30 {
		pool, err = pgxpool.New(ctx, cfg.DSN())
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				break
			}
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		return fmt.Errorf("could not connect to postgres: %w", err)
	}
	defer pool.Close()

	// Read migration files from disk
	files, err := filepath.Glob("db/timescale/migrations/*.sql")
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no migration files found in db/timescale/migrations/")
	}
	sort.Strings(files) // ensure order: 000001, 000002, etc.

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", f, err)
		}

		stmts := sqlutil.SplitStatements(sqlutil.GooseUpSection(string(data)))
		for _, stmt := range stmts {
			if _, err := pool.Exec(ctx, stmt); err != nil {
				// Ignore "already exists" errors
				if !strings.Contains(err.Error(), "already exists") &&
					!strings.Contains(err.Error(), "duplicate") {
					logger.Log.Debug().Err(err).Str("file", filepath.Base(f)).Msg("pg migration statement")
				}
			}
		}
		logger.Log.Info().Str("file", filepath.Base(f)).Msg("  applied")
	}

	return nil
}

// migrateScylla reads CQL files and creates keyspace + tables.
func migrateScylla(cfg config.ScyllaDBConfig) error {
	var session *gocql.Session
	var err error

	for range 60 {
		cluster := gocql.NewCluster(cfg.Hosts...)
		cluster.Timeout = 10 * time.Second
		cluster.ConnectTimeout = 10 * time.Second
		session, err = cluster.CreateSession()
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if err != nil {
		return fmt.Errorf("could not connect to scylladb: %w", err)
	}

	// Create keyspace
	if err := session.Query(`
		CREATE KEYSPACE IF NOT EXISTS ` + cfg.Keyspace + `
		WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}
	`).Exec(); err != nil {
		session.Close()
		return fmt.Errorf("create keyspace: %w", err)
	}
	session.Close()

	// Reconnect with keyspace
	cluster := gocql.NewCluster(cfg.Hosts...)
	cluster.Keyspace = cfg.Keyspace
	cluster.Timeout = 10 * time.Second
	session, err = cluster.CreateSession()
	if err != nil {
		return fmt.Errorf("connect to keyspace: %w", err)
	}
	defer session.Close()

	// Read CQL migration files
	files, err := filepath.Glob("db/scylla/migrations/*.cql")
	if err != nil || len(files) == 0 {
		logger.Log.Warn().Msg("no scylla migration files found, skipping")
		return nil
	}
	sort.Strings(files)

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", f, err)
		}

		// Strip full-line SQL comments so statements that start with a
		// comment line aren't silently dropped by the naive splitter below.
		var cleanedLines []string
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "--") {
				continue
			}
			cleanedLines = append(cleanedLines, line)
		}
		cleaned := strings.Join(cleanedLines, "\n")

		stmts := strings.Split(cleaned, ";")
		for _, stmt := range stmts {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" || strings.HasPrefix(strings.ToUpper(stmt), "USE ") || strings.HasPrefix(strings.ToUpper(stmt), "CREATE KEYSPACE") {
				continue
			}
			if err := session.Query(stmt).Exec(); err != nil {
				logger.Log.Debug().Err(err).Str("file", filepath.Base(f)).Msg("cql statement")
			}
		}
		logger.Log.Info().Str("file", filepath.Base(f)).Msg("  applied")
	}

	return nil
}
