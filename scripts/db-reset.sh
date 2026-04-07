#!/bin/bash
set -e

PG_CONTAINER="ndiscord-timescaledb"
SCYLLA_CONTAINER="ndiscord-scylladb"
REDIS_CONTAINER="ndiscord-redis"
PG_USER="ndiscord"
PG_DB="ndiscord"
SCYLLA_KEYSPACE="ndiscord"

echo "=== ndiscord DB Reset ==="

# ── Stop server first ──
echo ""
echo "[0/4] Stopping server..."
docker compose stop server voice 2>/dev/null || true

# ── TimescaleDB ──
echo ""
echo "[1/4] Resetting TimescaleDB..."

# Drop all tables (reverse order for FK deps)
docker exec "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -q -c "
DROP TABLE IF EXISTS media_files CASCADE;
DROP TABLE IF EXISTS webhooks CASCADE;
DROP TABLE IF EXISTS emojis CASCADE;
DROP TABLE IF EXISTS bans CASCADE;
DROP TABLE IF EXISTS invites CASCADE;
DROP TABLE IF EXISTS dm_channel_members CASCADE;
DROP TABLE IF EXISTS channel_permission_overwrites CASCADE;
DROP TABLE IF EXISTS channels CASCADE;
DROP TABLE IF EXISTS role_members CASCADE;
DROP TABLE IF EXISTS roles CASCADE;
DROP TABLE IF EXISTS guild_members CASCADE;
DROP TABLE IF EXISTS guilds CASCADE;
DROP TABLE IF EXISTS friendships CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS schema_migrations CASCADE;
"

# Run all up migrations
for f in db/timescale/migrations/*.up.sql; do
  echo "  Running: $(basename $f)"
  cat "$f" | docker exec -i "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -q
done
echo "  TimescaleDB: OK ($(docker exec "$PG_CONTAINER" psql -U "$PG_USER" -d "$PG_DB" -tAc "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public'") tables)"

# ── ScyllaDB ──
echo ""
echo "[2/4] Resetting ScyllaDB..."
docker exec "$SCYLLA_CONTAINER" cqlsh -e "DROP KEYSPACE IF EXISTS $SCYLLA_KEYSPACE;" 2>/dev/null || true
sleep 2

docker exec "$SCYLLA_CONTAINER" cqlsh -e \
  "CREATE KEYSPACE IF NOT EXISTS $SCYLLA_KEYSPACE WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1};" 2>/dev/null

docker exec "$SCYLLA_CONTAINER" cqlsh -k "$SCYLLA_KEYSPACE" -e \
  "CREATE TABLE IF NOT EXISTS messages (channel_id UUID, id TIMEUUID, author_id UUID, content TEXT, type INT, reply_to_id UUID, pinned BOOLEAN, edited_at TIMESTAMP, created_at TIMESTAMP, PRIMARY KEY (channel_id, id)) WITH CLUSTERING ORDER BY (id DESC);" 2>/dev/null
docker exec "$SCYLLA_CONTAINER" cqlsh -k "$SCYLLA_KEYSPACE" -e \
  "CREATE TABLE IF NOT EXISTS message_attachments (channel_id UUID, message_id TIMEUUID, id UUID, filename TEXT, url TEXT, content_type TEXT, size BIGINT, PRIMARY KEY ((channel_id, message_id), id));" 2>/dev/null
docker exec "$SCYLLA_CONTAINER" cqlsh -k "$SCYLLA_KEYSPACE" -e \
  "CREATE TABLE IF NOT EXISTS message_reactions (channel_id UUID, message_id TIMEUUID, emoji TEXT, user_id UUID, PRIMARY KEY ((channel_id, message_id), emoji, user_id));" 2>/dev/null
docker exec "$SCYLLA_CONTAINER" cqlsh -k "$SCYLLA_KEYSPACE" -e \
  "CREATE TABLE IF NOT EXISTS read_states (user_id UUID, channel_id UUID, last_read_message_id TIMEUUID, mention_count INT, PRIMARY KEY (user_id, channel_id));" 2>/dev/null
docker exec "$SCYLLA_CONTAINER" cqlsh -k "$SCYLLA_KEYSPACE" -e \
  "CREATE TABLE IF NOT EXISTS audit_log (guild_id UUID, id TIMEUUID, action_type INT, user_id UUID, target_id UUID, changes TEXT, created_at TIMESTAMP, PRIMARY KEY (guild_id, id)) WITH CLUSTERING ORDER BY (id DESC);" 2>/dev/null

echo "  ScyllaDB: OK (5 tables)"

# ── Redis ──
echo ""
echo "[3/4] Flushing Redis..."
docker exec "$REDIS_CONTAINER" redis-cli FLUSHALL >/dev/null
echo "  Redis: OK"

# ── Restart server ──
echo ""
echo "[4/4] Starting server..."
docker compose start server voice 2>/dev/null
sleep 3

echo ""
echo "=== All databases reset! ==="
echo ""
echo "  TimescaleDB : fresh schema (all migrations applied)"
echo "  ScyllaDB    : fresh keyspace + tables"
echo "  Redis       : flushed (sessions, tokens, presence, OTP)"
echo "  Server      : restarted"
