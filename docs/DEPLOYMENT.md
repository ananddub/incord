# Deployment

Local dev uses `docker-compose`; production is plain binaries plus
managed versions of the backing stores. There is no Kubernetes
manifest in the repo — keep the deployment surface simple.

## Local setup

1. **Dependencies** — Go 1.22+, Docker, Make, `buf`, `migrate`, `sqlc`.
2. **Bring up infra**:
   ```bash
   docker compose up -d
   ```
   Starts Postgres (Timescale), ScyllaDB, Redis, NATS, MinIO, LiveKit,
   OpenFGA, Prometheus and Grafana.
3. **Generate code** (only needed after a proto or SQL change):
   ```bash
   make generate          # buf generate + sqlc generate
   ```
4. **Run migrations**:
   ```bash
   make migrate           # applies db/timescale/migrations
   ```
5. **Copy env**:
   ```bash
   cp .env.example .env   # edit secrets (JWT_SECRET, SMTP_*)
   ```
6. **Run the server**:
   ```bash
   make run               # or: go run ./cmd/server
   ```
7. **Voice (separate binary if you want to run SFU locally)**:
   ```bash
   make run-voice
   ```

gRPC is on `:50051`, metrics + health + LiveKit webhook on `:9100`.

## Services in `docker-compose.yml`

| Service | Image | Port | Purpose |
|---|---|---|---|
| `postgres` | timescale/timescaledb | 5432 | relational store |
| `scylla` | scylladb/scylla | 9042 | messages |
| `redis` | redis:7 | 6379 | live state |
| `nats` | nats:2 | 4222 | pub/sub |
| `minio` | minio/minio | 9000 / 9001 | object storage + console |
| `livekit` | livekit/livekit-server | 7880 | WebRTC SFU |
| `openfga` | openfga/openfga | 8080 | authz |
| `prometheus` | prom/prometheus | 9090 | metrics |
| `grafana` | grafana/grafana | 3000 | dashboards |

Grafana provisioning loads dashboards from
[grafana/dashboards/](../grafana/dashboards) automatically.

## Environment variables

Configuration is env-only — there is no YAML config. All keys are
defined in [internal/shared/config/config.go](../internal/shared/config/config.go).

### Required

```
JWT_SECRET=<random 32+ bytes>
DB_HOST, DB_USER, DB_PASSWORD, DB_NAME
REDIS_ADDR
SCYLLA_HOST, SCYLLA_KEYSPACE
NATS_URL
MINIO_ENDPOINT, MINIO_ACCESS_KEY, MINIO_SECRET_KEY, MINIO_BUCKET
LIVEKIT_URL, LIVEKIT_HTTP_URL, LIVEKIT_API_KEY, LIVEKIT_API_SECRET
SMTP_HOST, SMTP_PORT, SMTP_USER, SMTP_PASSWORD, SMTP_FROM
```

### Optional / defaults

```
GRPC_PORT=50051                 # gRPC listen port
HOST=0.0.0.0                    # gRPC bind host
DB_SSLMODE=disable              # prod: require
DB_PORT=5432
REDIS_PASSWORD=                 # empty for local dev
REDIS_DB=0
OPENFGA_API_URL=http://localhost:8080
OPENFGA_STORE_ID=               # auto-provisions "ndiscord" if empty
JWT_ACCESS_TTL=168h             # 7 days
JWT_REFRESH_TTL=168h            # 7 days
INVITE_BASE_URL=https://example.com/invite
MINIO_PUBLIC_ENDPOINT=          # defaults to MINIO_ENDPOINT
MINIO_USE_SSL=false
```

## Migrations

- **Postgres**: plain `golang-migrate` files in
  [db/timescale/migrations/](../db/timescale/migrations). Named
  `000NNN_description.{up,down}.sql`.
- **Scylla**: `.cql` files in [db/scylla/migrations/](../db/scylla/migrations).
  Applied via `make scylla-migrate`.
- **OpenFGA**: the authz model is pushed by the app on startup if the
  store is empty (see [internal/shared/authz](../internal/shared/authz)).

**Adding a migration:**

```bash
migrate create -ext sql -dir db/timescale/migrations -seq description_of_change
```

Edit both `up.sql` and `down.sql`, commit both. Every migration is
idempotent where possible (`IF NOT EXISTS` on index creation).

## LiveKit wiring

LiveKit runs as its own service. The ndiscord server never holds a
persistent connection — it:

1. Creates rooms on-demand via `livekit-server-sdk-go` (HTTP).
2. Mints per-user JWT tokens and hands them back in `JoinChannel`
   responses.
3. Receives SFU events at `POST :9100/livekit/webhook`
   ([internal/features/voice/webhook.go](../internal/features/voice/webhook.go)).
   The webhook signature is verified with `LIVEKIT_API_SECRET`.

Configure the LiveKit side (in [livekit.yaml](../livekit.yaml)):

```yaml
webhook:
  api_key: ndiscord
  urls:
    - http://ndiscord-server:9100/livekit/webhook
```

## Observability

- Prometheus scrapes `ndiscord:9100/metrics`.
- Grafana has two boards:
  - `ndiscord` — RPC rate, active streams, registrations.
  - `ndiscord-nats` — per-subject publish/consume counts.
- Logs are zerolog JSON to stdout — pipe into Loki / Elasticsearch as
  you see fit.

## Production notes

- Terminate TLS in front of gRPC (nginx / envoy / fly.io).
- Redis and Postgres should be separate nodes — they're latency-
  sensitive hot paths. Scylla is throughput-sensitive; size it for
  message retention.
- MinIO should front with a CDN for the attachments bucket;
  `MINIO_PUBLIC_ENDPOINT` points at the CDN.
- NATS can run as a single node for small deployments; cluster it
  once you have multiple backend replicas (subjects are unchanged).
- OpenFGA supports Postgres as its backing store; point it at the
  same Postgres cluster (different database) in prod.
- There's no Redis Sentinel / cluster code — the client is a single
  address. Use a managed Redis (AWS ElastiCache, Upstash) in prod.

## Graceful shutdown

SIGINT/SIGTERM triggers `GracefulStop()` on the gRPC server. Open
streams finish naturally; the deferred presence cleanup in
`StreamFriendActivity` broadcasts `OFFLINE` for every connected user,
so clients see accurate state even on rolling deploys.

Give the pod a terminationGracePeriodSeconds of at least 30s to let
in-flight RPCs drain.
