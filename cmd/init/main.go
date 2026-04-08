package main

import (
	"context"
	"fmt"
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

	// [3/3] Redpanda topics
	log.Info().Msg("[3/3] Creating Redpanda topics...")
	if err := createTopics(cfg.Redpanda); err != nil {
		log.Warn().Err(err).Msg("Redpanda topic creation had errors (may already exist)")
	}
	log.Info().Msg("  Redpanda: done")

	log.Info().Msg("=== Init complete! ===")
}

func migratePG(ctx context.Context, cfg config.DatabaseConfig) error {
	// Wait for PG
	var pool *pgxpool.Pool
	var err error
	for i := 0; i < 30; i++ {
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

	migrations := []string{
		// 000001_init
		`CREATE EXTENSION IF NOT EXISTS "uuid-ossp"`,

		`CREATE TABLE IF NOT EXISTS users (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			username VARCHAR(32) UNIQUE NOT NULL,
			email VARCHAR(255) UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			avatar_url TEXT NOT NULL DEFAULT '',
			bio TEXT NOT NULL DEFAULT '',
			status VARCHAR(20) NOT NULL DEFAULT 'offline',
			verified BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS friendships (
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			friend_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			status VARCHAR(20) NOT NULL DEFAULT 'pending',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (user_id, friend_id)
		)`,

		`CREATE TABLE IF NOT EXISTS guilds (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name VARCHAR(100) NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			icon_url TEXT NOT NULL DEFAULT '',
			owner_id UUID NOT NULL REFERENCES users(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS guild_members (
			guild_id UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			nickname VARCHAR(32) NOT NULL DEFAULT '',
			joined_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (guild_id, user_id)
		)`,

		`CREATE TABLE IF NOT EXISTS roles (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			guild_id UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
			name VARCHAR(100) NOT NULL,
			color VARCHAR(7) NOT NULL DEFAULT '#99AAB5',
			position INT NOT NULL DEFAULT 0,
			permissions BIGINT NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS role_members (
			role_id UUID NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY (role_id, user_id)
		)`,

		`CREATE TABLE IF NOT EXISTS channels (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			guild_id UUID REFERENCES guilds(id) ON DELETE CASCADE,
			name VARCHAR(100) NOT NULL,
			type INT NOT NULL DEFAULT 1,
			topic TEXT NOT NULL DEFAULT '',
			position INT NOT NULL DEFAULT 0,
			parent_id UUID REFERENCES channels(id) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS channel_permission_overwrites (
			channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
			target_id UUID NOT NULL,
			target_type VARCHAR(10) NOT NULL,
			allow_bits BIGINT NOT NULL DEFAULT 0,
			deny_bits BIGINT NOT NULL DEFAULT 0,
			PRIMARY KEY (channel_id, target_id)
		)`,

		`CREATE TABLE IF NOT EXISTS dm_channel_members (
			channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY (channel_id, user_id)
		)`,

		`CREATE TABLE IF NOT EXISTS invites (
			code VARCHAR(10) PRIMARY KEY,
			guild_id UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
			channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
			creator_id UUID NOT NULL REFERENCES users(id),
			max_uses INT NOT NULL DEFAULT 0,
			uses INT NOT NULL DEFAULT 0,
			expires_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS bans (
			guild_id UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
			user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			reason TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (guild_id, user_id)
		)`,

		`CREATE TABLE IF NOT EXISTS emojis (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			guild_id UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
			name VARCHAR(32) NOT NULL,
			image_url TEXT NOT NULL,
			creator_id UUID NOT NULL REFERENCES users(id),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS webhooks (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
			guild_id UUID NOT NULL REFERENCES guilds(id) ON DELETE CASCADE,
			name VARCHAR(80) NOT NULL,
			avatar_url TEXT NOT NULL DEFAULT '',
			token TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		`CREATE TABLE IF NOT EXISTS media_files (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			uploader_id UUID NOT NULL REFERENCES users(id),
			filename TEXT NOT NULL,
			content_type VARCHAR(255) NOT NULL,
			size BIGINT NOT NULL,
			bucket_key TEXT NOT NULL,
			confirmed BOOLEAN NOT NULL DEFAULT FALSE,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Indexes (IF NOT EXISTS not supported for indexes, so use DO block)
		`CREATE INDEX IF NOT EXISTS idx_users_username ON users(username)`,
		`CREATE INDEX IF NOT EXISTS idx_users_email ON users(email)`,
		`CREATE INDEX IF NOT EXISTS idx_friendships_friend ON friendships(friend_id)`,
		`CREATE INDEX IF NOT EXISTS idx_guild_members_user ON guild_members(user_id)`,
		`CREATE INDEX IF NOT EXISTS idx_roles_guild ON roles(guild_id)`,
		`CREATE INDEX IF NOT EXISTS idx_channels_guild ON channels(guild_id)`,
		`CREATE INDEX IF NOT EXISTS idx_dm_members_user ON dm_channel_members(user_id)`,
	}

	for _, m := range migrations {
		if _, err := pool.Exec(ctx, m); err != nil {
			// Ignore "already exists" errors
			logger.Log.Debug().Err(err).Msg("migration statement (may already exist)")
		}
	}

	return nil
}

func migrateScylla(cfg config.ScyllaDBConfig) error {
	// Wait for ScyllaDB
	var session *gocql.Session
	var err error

	// First connect without keyspace to create it
	for i := 0; i < 60; i++ {
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
		CREATE KEYSPACE IF NOT EXISTS ndiscord
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

	tables := []string{
		`CREATE TABLE IF NOT EXISTS messages (
			channel_id UUID, id TIMEUUID, author_id UUID, content TEXT, type INT,
			reply_to_id UUID, pinned BOOLEAN, edited_at TIMESTAMP, created_at TIMESTAMP,
			PRIMARY KEY (channel_id, id)
		) WITH CLUSTERING ORDER BY (id DESC)`,

		`CREATE TABLE IF NOT EXISTS message_attachments (
			channel_id UUID, message_id TIMEUUID, id UUID, filename TEXT, url TEXT,
			content_type TEXT, size BIGINT,
			PRIMARY KEY ((channel_id, message_id), id)
		)`,

		`CREATE TABLE IF NOT EXISTS message_reactions (
			channel_id UUID, message_id TIMEUUID, emoji TEXT, user_id UUID,
			PRIMARY KEY ((channel_id, message_id), emoji, user_id)
		)`,

		`CREATE TABLE IF NOT EXISTS read_states (
			user_id UUID, channel_id UUID, last_read_message_id TIMEUUID, mention_count INT,
			PRIMARY KEY (user_id, channel_id)
		)`,

		`CREATE TABLE IF NOT EXISTS audit_log (
			guild_id UUID, id TIMEUUID, action_type INT, user_id UUID, target_id UUID,
			changes TEXT, created_at TIMESTAMP,
			PRIMARY KEY (guild_id, id)
		) WITH CLUSTERING ORDER BY (id DESC)`,
	}

	for _, t := range tables {
		if err := session.Query(t).Exec(); err != nil {
			logger.Log.Debug().Err(err).Msg("scylla table (may already exist)")
		}
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
