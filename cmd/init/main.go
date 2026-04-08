package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/gocql/gocql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

func main() {
	logger.Init("info")
	log := logger.Log
	cfg := config.Load()
	ctx := context.Background()

	log.Info().Msg("=== ndiscord init ===")

	// [1/4] TimescaleDB
	log.Info().Msg("[1/4] Running TimescaleDB migrations...")
	if err := migratePG(ctx, cfg.Database); err != nil {
		log.Fatal().Err(err).Msg("TimescaleDB migration failed")
	}
	log.Info().Msg("  TimescaleDB: done")

	// [2/4] ScyllaDB
	log.Info().Msg("[2/4] Setting up ScyllaDB...")
	if err := migrateScylla(cfg.ScyllaDB); err != nil {
		log.Fatal().Err(err).Msg("ScyllaDB setup failed")
	}
	log.Info().Msg("  ScyllaDB: done")

	// [3/4] Redpanda topics
	log.Info().Msg("[3/4] Creating Redpanda topics...")
	if err := createTopics(cfg.Redpanda); err != nil {
		log.Warn().Err(err).Msg("Redpanda topic creation had errors (may already exist)")
	}
	log.Info().Msg("  Redpanda: done")

	// [4/4] OpenFGA
	log.Info().Msg("[4/4] Setting up OpenFGA...")
	if err := setupOpenFGA(cfg.OpenFGA); err != nil {
		log.Warn().Err(err).Msg("OpenFGA setup failed (auth will be permissive)")
	} else {
		log.Info().Msg("  OpenFGA: done")
	}

	log.Info().Msg("=== Init complete! ===")
}

// migratePG reads all .up.sql files from db/timescale/migrations/ and executes them.
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
	files, err := filepath.Glob("db/timescale/migrations/*.up.sql")
	if err != nil || len(files) == 0 {
		return fmt.Errorf("no migration files found in db/timescale/migrations/")
	}
	sort.Strings(files) // ensure order: 000001, 000002, etc.

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", f, err)
		}

		// Split by semicolons and execute each statement
		stmts := strings.Split(string(data), ";")
		for _, stmt := range stmts {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
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

		stmts := strings.Split(string(data), ";")
		for _, stmt := range stmts {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" || strings.HasPrefix(stmt, "--") || strings.HasPrefix(strings.ToUpper(stmt), "USE ") || strings.HasPrefix(strings.ToUpper(stmt), "CREATE KEYSPACE") {
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

func createTopics(cfg config.RedpandaConfig) error {
	client, err := kgo.NewClient(kgo.SeedBrokers(cfg.Brokers...))
	if err != nil {
		return err
	}
	defer client.Close()

	admin := kadm.NewClient(client)

	topics := []string{
		"message.create", "message.update", "message.delete",
		"guild.events", "channel.events",
		"presence.update", "typing.start", "voice.state", "user.update",
	}

	for _, topic := range topics {
		_, err := admin.CreateTopic(context.Background(), 1, 1, nil, topic)
		if err != nil {
			logger.Log.Debug().Err(err).Str("topic", topic).Msg("topic create (may already exist)")
		}
	}

	return nil
}

func setupOpenFGA(cfg config.OpenFGAConfig) error {
	apiURL := cfg.APIUrl
	if apiURL == "" {
		apiURL = "http://localhost:8090"
	}

	for range 15 {
		resp, err := http.Get(apiURL + "/healthz")
		if err == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(time.Second)
	}

	resp, err := http.Get(apiURL + "/healthz")
	if err != nil {
		return fmt.Errorf("openfga not reachable: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("SERVING")) {
		return fmt.Errorf("openfga not serving: %s", string(body))
	}

	// Check if store exists
	storesResp, err := http.Get(apiURL + "/stores")
	if err != nil {
		return fmt.Errorf("failed to list stores: %w", err)
	}
	defer storesResp.Body.Close()
	var storesBody struct {
		Stores []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"stores"`
	}
	json.NewDecoder(storesResp.Body).Decode(&storesBody)

	var storeID string
	for _, s := range storesBody.Stores {
		if s.Name == "ndiscord" {
			storeID = s.ID
			break
		}
	}

	if storeID == "" {
		payload, _ := json.Marshal(map[string]string{"name": "ndiscord"})
		cr, err := http.Post(apiURL+"/stores", "application/json", bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create store: %w", err)
		}
		defer cr.Body.Close()
		var created struct{ ID string `json:"id"` }
		json.NewDecoder(cr.Body).Decode(&created)
		storeID = created.ID
		logger.Log.Info().Str("store_id", storeID).Msg("  OpenFGA store created")
	} else {
		logger.Log.Info().Str("store_id", storeID).Msg("  OpenFGA store exists")
	}

	// Read model from file if exists, otherwise use default
	model := getOpenFGAModel()

	mr, err := http.Post(apiURL+"/stores/"+storeID+"/authorization-models", "application/json", bytes.NewReader([]byte(model)))
	if err != nil {
		return fmt.Errorf("create model: %w", err)
	}
	defer mr.Body.Close()
	var modelResult struct{ AuthorizationModelID string `json:"authorization_model_id"` }
	json.NewDecoder(mr.Body).Decode(&modelResult)
	logger.Log.Info().Str("model_id", modelResult.AuthorizationModelID).Msg("  OpenFGA auth model set")

	return nil
}

func getOpenFGAModel() string {
	// Try reading from file first
	if data, err := os.ReadFile("openfga/model.json"); err == nil {
		return string(data)
	}
	// Fallback to embedded default
	return `{"schema_version":"1.1","type_definitions":[{"type":"user","relations":{}},{"type":"guild","relations":{"owner":{"this":{}},"admin":{"this":{}},"member":{"this":{}},"can_manage_guild":{"union":{"child":[{"computedUserset":{"relation":"owner"}},{"computedUserset":{"relation":"admin"}}]}},"can_kick":{"union":{"child":[{"computedUserset":{"relation":"owner"}},{"computedUserset":{"relation":"admin"}}]}},"can_ban":{"union":{"child":[{"computedUserset":{"relation":"owner"}},{"computedUserset":{"relation":"admin"}}]}},"can_manage_roles":{"union":{"child":[{"computedUserset":{"relation":"owner"}},{"computedUserset":{"relation":"admin"}}]}},"can_manage_channels":{"union":{"child":[{"computedUserset":{"relation":"owner"}},{"computedUserset":{"relation":"admin"}}]}}},"metadata":{"relations":{"owner":{"directly_related_user_types":[{"type":"user"}]},"admin":{"directly_related_user_types":[{"type":"user"}]},"member":{"directly_related_user_types":[{"type":"user"}]},"can_manage_guild":{},"can_kick":{},"can_ban":{},"can_manage_roles":{},"can_manage_channels":{}}}},{"type":"channel","relations":{"guild":{"this":{}},"viewer":{"union":{"child":[{"this":{}},{"tupleToUserset":{"tupleset":{"relation":"guild"},"computedUserset":{"relation":"member"}}}]}},"sender":{"union":{"child":[{"this":{}},{"tupleToUserset":{"tupleset":{"relation":"guild"},"computedUserset":{"relation":"member"}}}]}},"manager":{"union":{"child":[{"this":{}},{"tupleToUserset":{"tupleset":{"relation":"guild"},"computedUserset":{"relation":"can_manage_channels"}}}]}}},"metadata":{"relations":{"guild":{"directly_related_user_types":[{"type":"guild"}]},"viewer":{"directly_related_user_types":[{"type":"user"}]},"sender":{"directly_related_user_types":[{"type":"user"}]},"manager":{"directly_related_user_types":[{"type":"user"}]}}}}]}`
}
