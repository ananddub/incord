# ndiscord_backend

Discord clone backend built from scratch in Go. Feature-based monolith architecture with gRPC APIs, real-time bidirectional streaming, and a custom UDP voice/video/screen sharing server.

## Tech Stack

| Component | Technology | Purpose |
|---|---|---|
| Language | Go 1.24 | Core language |
| API | gRPC + Protobuf | All client-server and internal communication |
| Real-time | gRPC bidirectional streaming | Gateway for live events (messages, typing, presence, etc.) |
| Auth | JWT + bcrypt | Stateless access tokens, refresh tokens in Redis |
| Primary DB | TimescaleDB (PostgreSQL) | Users, guilds, channels, roles, invites, bans, media metadata |
| Message DB | ScyllaDB | Messages, reactions, read states, audit log (high-throughput) |
| Cache/State | Redis | Sessions, presence, typing indicators, voice state, refresh tokens |
| Events | Redpanda (Kafka-compatible) | Event streaming between services and gateway |
| Storage | MinIO (S3-compatible) | File uploads, avatars, attachments |
| Auth Model | OpenFGA | Fine-grained authorization (ready to wire) |
| Monitoring | Grafana + Prometheus | Metrics and dashboards |
| SQL Codegen | sqlc | Type-safe Go from SQL queries |
| Logging | zerolog | Structured JSON logging |
| Voice | Custom UDP | SFU-based audio/video/screen sharing |
| Dev | Air | Hot-reload in Docker |

## Architecture

```
                    gRPC Clients
                        |
                   gRPC Server (:50051)
                   (auth + logging middleware)
                        |
         +---------+---------+---------+---------+
         |         |         |         |         |
       Auth      User     Guild   Channel   Message  ...
      Feature   Feature   Feature  Feature   Feature
         |         |         |         |         |
         +----+----+----+----+    +----+----+
              |              |    |         |
         TimescaleDB      Redis  ScyllaDB  MinIO
              |
           OpenFGA
```

### Feature-Based Structure

Each feature is self-contained with its own handler, service, repository, and proto:

```
internal/features/{feature}/
  handler.go       # gRPC handler - request validation, error mapping
  service.go       # Business logic - rules, workflows
  repository.go    # Data access - DB queries, Redis ops
  model.go         # Domain models, errors
  proto/{feature}/v1/{feature}.proto
  handler_test.go  # gRPC integration test (real server + client + DB)
  service_test.go  # Service integration test (real DB)
```

### Data Flow: Sending a Message

```
1. Client sends gRPC: MessageService.SendMessage
2. Auth middleware validates JWT from metadata
3. Handler validates input, extracts user_id from context
4. Service generates TimeUUID, calls repository
5. Repository writes to ScyllaDB
6. Service publishes event to Redpanda (topic: message.create)
7. Dispatcher consumes from Redpanda
8. Dispatcher looks up gateway sessions for the guild
9. Dispatcher pushes ServerEvent to each session's Send channel
10. Gateway streams the event to connected clients
```

### Real-Time Gateway

Clients connect via `GatewayService.Connect` - a gRPC bidirectional stream.

**Client -> Server messages:**
- `Identify` - authenticate with JWT, receive `Ready` event with session info
- `Heartbeat` - keep connection alive (45s interval)
- `Resume` - reconnect with existing session
- `UpdatePresence` - change online status
- `UpdateVoiceState` - join/leave voice channels

**Server -> Client events:**
- `MessageCreate/Update/Delete` - real-time messages
- `TypingStart` - typing indicators
- `PresenceUpdate` - friend online/offline/idle/dnd
- `GuildCreate/Update/Delete` - guild changes
- `GuildMemberAdd/Remove` - member joins/leaves
- `ChannelCreate/Update/Delete` - channel changes
- `VoiceStateUpdate` - voice channel activity
- `VoiceServerUpdate` - UDP endpoint for voice

### Voice/Video/Screen Sharing

Custom UDP protocol with SFU (Selective Forwarding Unit) architecture:

```
cmd/voice/main.go  ->  UDP Server (:50052)
```

**UDP Packet Format (14-byte header):**
```
| Version(1) | Type(1) | Sequence(2) | Timestamp(4) | SSRC(4) | PayloadLen(2) | Payload... |
```

**Packet Types:**
- `0x01` Audio (Opus codec)
- `0x02` Video (VP8/VP9)
- `0x03` Screen share
- `0x04` Heartbeat
- `0x05` Handshake

**Flow:**
1. Client calls `VoiceService.JoinChannel` via gRPC -> gets UDP endpoint, SSRC, encryption key
2. Client sends UDP packets to voice server
3. Voice server forwards packets to all other participants in the same channel (SFU model - no mixing)

## Project Structure

```
ndiscord/
  cmd/
    server/main.go           # Main gRPC server - wires all features
    voice/main.go            # UDP voice server
  internal/
    features/
      auth/                  # Register, Login, JWT, Refresh tokens
      user/                  # Profiles, Friends, Block
      guild/                 # Servers, Members, Roles, Invites, Bans
      channel/               # Text, Voice, Video, Category, DM, Group DM
      message/               # Messages, Reactions, Pins, Typing, Read states
      gateway/               # Bidirectional streaming, Sessions, Dispatcher
      presence/              # Online/Offline/Idle/DND status
      media/                 # File uploads via MinIO presigned URLs
      voice/                 # Voice sessions, UDP protocol, SFU server
    shared/
      config/                # Environment-based configuration
      db/                    # PostgreSQL, Redis, ScyllaDB, MinIO connections
      event/                 # Redpanda producer + consumer
      middleware/             # Auth (JWT) + Logging gRPC interceptors
      logger/                # zerolog setup
      testutil/              # Testcontainer helpers (PG + Redis)
  db/
    timescale/
      migrations/            # PostgreSQL schema (users, guilds, channels, etc.)
      queries/               # sqlc SQL queries
      sqlc.yaml              # sqlc configuration
    scylla/
      migrations/            # ScyllaDB schema (messages, reactions, read_states)
  gen/                       # Generated code (proto + sqlc) - do not edit
  docker-compose.yml         # All infra + app with hot-reload
  Dockerfile.dev             # Dev container with Air
  Makefile                   # Build, run, test, generate commands
  buf.yaml                   # Protobuf configuration
```

## Quick Start

### Prerequisites

- Go 1.24+
- Docker + Docker Compose
- [buf](https://buf.build/docs/installation) (protobuf toolchain)
- [sqlc](https://docs.sqlc.dev/en/latest/overview/install.html)

### Run Everything

```bash
# Start all infrastructure + app (hot-reload enabled)
docker compose up

# Server: localhost:50051 (gRPC)
# Voice:  localhost:50052 (UDP)
```

That's it. Air watches for file changes and auto-rebuilds inside the container.

### Run Locally (without Docker for app)

```bash
# Start infra only
docker compose up timescaledb redis scylladb redpanda minio openfga -d

# Run migrations
make migrate-up

# Run server
make run

# Run voice server (separate terminal)
make run-voice
```

### Generate Code

```bash
# Generate proto Go code + sqlc queries
make generate

# Or individually
make proto    # protobuf
make sqlc     # SQL queries
```

### Run Tests

```bash
# All tests (needs Docker for testcontainers)
make test

# Specific feature
go test ./internal/features/auth/... -v

# Pure Go tests (no infra needed)
go test ./internal/features/gateway/... ./internal/features/voice/udp/... -v
```

Tests use [testcontainers-go](https://github.com/testcontainers/testcontainers-go) to spin up real databases:
- **TimescaleDB** for PostgreSQL tests
- **Redis** for cache/state tests
- **ScyllaDB** for message tests

No mocking of infrastructure. Every test hits real databases.

## Database Schema

### TimescaleDB (PostgreSQL)

| Table | Purpose |
|---|---|
| `users` | User accounts (username, email, password_hash, avatar, bio) |
| `friendships` | Friend requests, accepted friends, blocks |
| `guilds` | Servers (name, description, icon, owner) |
| `guild_members` | Guild membership (guild_id, user_id, nickname) |
| `roles` | Guild roles (name, color, position, permissions bitfield) |
| `role_members` | Role assignments |
| `channels` | Text/Voice/Video/Category/DM/GroupDM channels |
| `dm_channel_members` | DM channel participants |
| `invites` | Guild invite codes (max_uses, expiry) |
| `bans` | Guild bans with reason |
| `emojis` | Custom guild emojis |
| `webhooks` | Channel webhooks |
| `media_files` | Uploaded file metadata (bucket_key, confirmed) |

### ScyllaDB

| Table | Partition Key | Clustering | Purpose |
|---|---|---|---|
| `messages` | channel_id | id DESC | Messages (time-sorted) |
| `message_reactions` | (channel_id, message_id) | emoji, user_id | Reactions |
| `read_states` | user_id | channel_id | Last read position |
| `audit_log` | guild_id | id DESC | Audit trail |

### Redis

| Key Pattern | Type | Purpose |
|---|---|---|
| `refresh:{token}` | String | Refresh token -> user_id |
| `presence:{user_id}` | Hash | Online status, custom status, last_seen |
| `typing:{channel_id}` | String | Typing indicator (8s TTL) |
| `voice:{channel_id}` | Set | Voice channel participants |
| `voicestate:{session_id}` | Hash | Mute/deaf/video/stream state |

## gRPC Services

| Service | Port | Methods |
|---|---|---|
| AuthService | 50051 | Register, Login, RefreshToken, Logout, ValidateToken |
| UserService | 50051 | GetUser, UpdateUser, DeleteUser, SearchUsers, Friends (9 RPCs) |
| GuildService | 50051 | CRUD, Join/Leave, Kick/Ban, Roles, Invites (17 RPCs) |
| ChannelService | 50051 | CRUD, DM channels (7 RPCs) |
| MessageService | 50051 | Send/Edit/Delete, Reactions, Pin, Typing, Ack (11 RPCs) |
| GatewayService | 50051 | Connect (bidirectional stream) |
| PresenceService | 50051 | Update, Get, GetBulk (3 RPCs) |
| MediaService | 50051 | RequestUpload, ConfirmUpload, GetDownloadURL, Delete (4 RPCs) |
| VoiceService | 50051 | JoinChannel, LeaveChannel, GetParticipants, UpdateState (4 RPCs) |

All services share one gRPC server with auth + logging middleware.

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `GRPC_PORT` | 50051 | gRPC server port |
| `DB_HOST` | localhost | TimescaleDB host |
| `DB_PORT` | 5432 | TimescaleDB port |
| `DB_USER` | ndiscord | Database user |
| `DB_PASSWORD` | ndiscord | Database password |
| `DB_NAME` | ndiscord | Database name |
| `REDIS_ADDR` | localhost:6379 | Redis address |
| `SCYLLA_HOST` | localhost:9042 | ScyllaDB host |
| `REDPANDA_BROKER` | localhost:9092 | Redpanda broker |
| `MINIO_ENDPOINT` | localhost:9000 | MinIO endpoint |
| `MINIO_ACCESS_KEY` | ndiscord | MinIO access key |
| `MINIO_SECRET_KEY` | ndiscord123 | MinIO secret key |
| `OPENFGA_API_URL` | http://localhost:8090 | OpenFGA API |
| `JWT_SECRET` | change-me-in-production | JWT signing secret |
| `VOICE_UDP_HOST` | 0.0.0.0 | Voice UDP bind host |
| `VOICE_UDP_PORT` | 50052 | Voice UDP bind port |

## Docker Services

| Service | Port | UI |
|---|---|---|
| ndiscord-server | 50051 (gRPC) | - |
| ndiscord-voice | 50052 (UDP) | - |
| TimescaleDB | 5432 | - |
| Redis | 6379 | - |
| ScyllaDB | 9042 | - |
| Redpanda | 9092 | Console: localhost:8080 |
| MinIO | 9000 | Console: localhost:9001 |
| OpenFGA | 8090 | Playground: localhost:3000 |
| Grafana | 3001 | localhost:3001 (admin/admin) |
| Prometheus | 9090 | localhost:9090 |

## Testing

171 integration tests across all features. Every test hits real infrastructure via testcontainers.

```
Feature      | Tests | What's Tested
-------------|-------|------------------------------------------
Auth         |    24 | Register, Login, JWT, Refresh rotation, Logout
User         |    30 | Profiles, Friends, Block, Search (gRPC e2e)
Guild        |    15 | CRUD, Roles, Invites, Ban/Kick, Permissions
Channel      |    15 | Guild channels, DMs, Group DMs
Message      |    29 | CRUD, Reactions, Pins, Typing, Pagination (ScyllaDB)
Gateway      |    20 | Session manager, Event dispatcher routing
Presence     |    12 | Status updates, Bulk queries (Redis)
Media        |     5 | Upload/Confirm/Delete flow (PostgreSQL)
Voice        |    21 | Join/Leave, Participants, UDP encode/decode
-------------|-------|
Total        |   171 |
```

## Make Commands

```bash
make build          # Build server + voice binaries
make run            # Build and run server
make run-voice      # Build and run voice server
make generate       # Generate proto + sqlc code
make proto          # Generate protobuf Go code
make sqlc           # Generate sqlc Go code
make migrate-up     # Run TimescaleDB migrations
make migrate-down   # Rollback last migration
make test           # Run all tests
make test-coverage  # Tests with coverage report
make lint           # Run golangci-lint
make docker-up      # Start all Docker services
make docker-down    # Stop all Docker services
make docker-logs    # Follow Docker logs
make clean          # Remove build artifacts
```
