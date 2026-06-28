# Architecture

High-level design for the ndiscord Go backend — a Discord-style chat,
voice and guild platform exposed as a gRPC API.

## 10,000-ft view

```
                ┌────────────┐
                │  Clients   │  (gRPC / gRPC-Web)
                └──────┬─────┘
                       │  Bearer JWT in metadata
                       ▼
  ┌────────────────────────────────────────────────────────┐
  │  gRPC server :50051  (cmd/server/main.go)              │
  │  ├── unary interceptors:  log → ratelimit → auth → val │
  │  └── stream interceptors: log → auth                   │
  │                                                        │
  │  10 feature services registered on one server:         │
  │   Auth · User · Guild · Channel · Message · Voice      │
  │   Stream · Sync · Presence · Media                     │
  └────┬───────────┬─────────┬──────────┬────────┬────────┘
       │           │         │          │        │
       ▼           ▼         ▼          ▼        ▼
  ┌─────────┐ ┌───────┐ ┌────────┐ ┌───────┐ ┌────────┐
  │Postgres │ │Scylla │ │  Redis │ │ NATS  │ │ MinIO  │
  │(Timescale)│ │messages│ │live state│ │pub/sub│ │objects │
  └─────────┘ └───────┘ └────────┘ └───┬───┘ └────────┘
                                       │
                 ┌─────────────────────┤
                 ▼                     ▼
                              ┌──────────┐
                              │ LiveKit  │
                              │  (voice) │
                              └──────────┘

HTTP :9100
  ├─ /metrics         (Prometheus)
  ├─ /health          (liveness)
  └─ /livekit/webhook (SFU events → NATS)
```

## Entry point

[cmd/server/main.go](../cmd/server/main.go) is the single binary:

1. `config.Load()` reads env vars into a typed [`Config`](../internal/shared/config/config.go).
2. `app.NewInfra()` ([internal/app/infra.go](../internal/app/infra.go))
   dials every backing service (Postgres, Scylla, Redis, NATS, MinIO)
   and exposes them on a struct. Required stores abort startup if they
   can't be reached.
3. `app.NewServices()` ([internal/app/services.go](../internal/app/services.go))
   wires the feature services together (cross-feature interfaces like
   `DMOpener`, `PresenceController`, `MediaResolver`, …).
4. `app.NewServer()` registers the gRPC services and starts the server
   on `GRPC_PORT` (default `50051`).
5. `StartMetricsServer` starts an HTTP server on `:9100`
   ([internal/shared/metrics](../internal/shared/metrics)) exposing
   `/metrics`, `/health`, and `/livekit/webhook`.
6. A `SIGINT` / `SIGTERM` handler drains gRPC via `GracefulStop`.

## Backing services

| Store | Purpose | Config prefix |
|---|---|---|
| **Postgres** (Timescale) | durable entities: users, guilds, channels, roles, invites, bans, media metadata | `DB_*` |
| **ScyllaDB** | messages, reactions, read states, audit log — time-series, high-throughput | `SCYLLA_*` |
| **Redis** | sessions, presence, typing, voice state, rate-limit counters, OTPs | `REDIS_*` |
| **NATS** | realtime pub/sub for events streamed out through `StreamService` | `NATS_URL` |
| **MinIO** | S3-compatible object storage for avatars, guild icons, attachments | `MINIO_*` |
| **LiveKit** | WebRTC SFU for voice/video/screen share | `LIVEKIT_*` |
| **SMTP** | email OTP for signup verification | `SMTP_*` |

Every dependency is owned by `Infra` and passed into services by
constructor injection — no global state, no package-level singletons.

## Request pipeline

### Unary RPC

```
client → Logging → RateLimit → Auth → Validation → handler → repo → store
                                 │
                                 └── UserIDKey stashed in ctx
```

- [middleware/logging.go](../internal/shared/middleware/logging.go) —
  structured zerolog line per call with method + duration.
- [middleware/ratelimit.go](../internal/shared/middleware/ratelimit.go)
  — sliding-window counter in Redis keyed by `<peer or userID>:<method>`.
  Auth endpoints use peer IP; everything else uses the JWT subject.
- [middleware/auth.go](../internal/shared/middleware/auth.go) — reads
  `authorization: Bearer <jwt>`, verifies HMAC-SHA256 with `JWT_SECRET`,
  puts the `sub` claim under `UserIDKey` in `ctx`. Public methods
  (`/auth.v1.AuthService/*`, `GuildService/PreviewInvite`) bypass this.
- Validation is Buf's protobuf validator, zero-setup.

### Server-streaming RPC

Only the stream interceptors apply (logging + auth). Handlers in
[stream/handler.go](../internal/features/stream/handler.go) pull the
`UserIDKey` out of `stream.Context()`, resolve the subjects the user
is allowed to see (own guilds, own friends, own DMs) and subscribe to
NATS. Messages are JSON-unmarshalled directly into the generated proto
struct and forwarded via `stream.Send(…)`.

## Real-time events

All "something happened" fan-out goes through NATS. A write RPC does
its Postgres/Scylla/Redis work and then publishes one typed proto
payload on a well-known subject. `StreamService` is the only consumer
that reaches the outside world — clients subscribe by RPC, not by
connecting to NATS directly.

Subjects are catalogued in [NATS_SUBJECTS.md](./NATS_SUBJECTS.md).

Key property: **publishers are strongly typed.** Every `Publish` call
takes a `*streamv1.Something` proto (no `map[string]any`), and every
string-tag field (event, action, type, status) is a proto enum, so a
typo or missed rename fails at compile time.

## Observability

| Signal | Where |
|---|---|
| **Logs** | zerolog JSON to stdout (interceptors + per-feature) |
| **Metrics** | `http://:9100/metrics` — counters for RPCs, events, active streams; gauges for active sessions |
| **Dashboards** | [grafana/dashboards/](../grafana/dashboards) — NATS + ndiscord service boards |
| **Alerts** | Prometheus rules not checked in; run via `prometheus.yml` at repo root |
| **Traces** | OTel SDK vendored but not wired; TODO |
| **Health** | `GET /health` returns `ok` |

## Authn / Authz split

- **Authentication** (are you who you say you are?) is handled entirely
  inside [features/auth](../internal/features/auth): bcrypt passwords,
  JWT access + refresh tokens, SMTP OTP for email verification, Redis
  refresh-token store.
- **Authorization** (can you do this thing?) is a thin Postgres-backed
  RBAC in [authz.Client](../internal/shared/authz/client.go). Three
  tables (`roles`, `permissions`, `role_permissions`) plus the
  `guilds.owner_id` column decide every check. Guild owner always
  passes; an `ADMINISTRATOR` row in `role_permissions` short-circuits
  specific-permission lookups. Every `Can*` check is one indexed SQL
  query. Services never read `guild_members` directly for policy —
  they call the authz layer, which keeps the decision logic in one
  place.

## Graceful shutdown

`main.go` wires a `signal.Notify` on SIGINT/SIGTERM and calls
`GracefulStop()` on the gRPC server. Open streams (StreamVoiceState,
StreamFriendActivity, …) finish naturally on `ctx.Done()`. The
StreamFriendActivity handler's deferred `OnUserDisconnect` fires a
presence_update offline event to friends on the way out.

## Where to look next

- Per-feature internals → [features/](./features)
- Storage schemas → [DATA_MODEL.md](./DATA_MODEL.md)
- NATS subject catalog → [NATS_SUBJECTS.md](./NATS_SUBJECTS.md)
- Local setup → [DEPLOYMENT.md](./DEPLOYMENT.md)
- Public API → [API_GUIDE.md](./API_GUIDE.md)
