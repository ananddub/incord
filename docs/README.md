# ndiscord Documentation

Internal design docs for the ndiscord Go backend. Start with the top
of this page for a mental model, then drop into whichever feature
you're working on.

## Top-level

| Doc | What it covers |
|---|---|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | System overview, backing services, request pipeline, graceful shutdown |
| [DATA_MODEL.md](./DATA_MODEL.md) | Postgres tables, Scylla tables, Redis keys, MinIO paths, OpenFGA tuples |
| [NATS_SUBJECTS.md](./NATS_SUBJECTS.md) | Wire-level event catalog — subjects, publishers, payload protos |
| [DEPLOYMENT.md](./DEPLOYMENT.md) | Local dev setup, env vars, migrations, production notes |
| [API_GUIDE.md](./API_GUIDE.md) | Public gRPC API reference for client developers |

## Features (LLD)

Each feature doc follows the same shape: gRPC surface, data owned,
key flows, events emitted, dependencies, failure modes.

| Feature | Doc |
|---|---|
| Auth / JWT / OTP | [features/auth.md](./features/auth.md) |
| User profile + friend graph | [features/user.md](./features/user.md) |
| Guilds (servers), invites, roles | [features/guild.md](./features/guild.md) |
| Channels (guild + DM) | [features/channel.md](./features/channel.md) |
| Messages, reactions, typing, pins | [features/message.md](./features/message.md) |
| Voice state + LiveKit bridge | [features/voice.md](./features/voice.md) |
| Realtime event stream gateway | [features/stream.md](./features/stream.md) |
| Presence (online/offline) | [features/presence.md](./features/presence.md) |
| Media upload orchestration | [features/media.md](./features/media.md) |
| Bulk client hydration | [features/sync.md](./features/sync.md) |

## Reading order

**New to the codebase?** → ARCHITECTURE → DATA_MODEL →
features/auth → features/stream → one feature you're working on.

**Adding a new RPC?** → feature's LLD doc → DATA_MODEL if it touches
storage → NATS_SUBJECTS if it emits events.

**Debugging a realtime issue?** → NATS_SUBJECTS for the subject path
→ features/stream for subscriber behaviour → relevant feature for
the publish call site.

## Conventions

- **Typed protos everywhere.** NATS publishes take a
  `*streamv1.Something`, never `map[string]any`. String-tag fields
  (event / type / action / status) are proto enums.
- **Soft deletes.** Every Postgres entity that can be "deleted" has
  a `deleted BOOLEAN` column. Code never issues `DELETE` except in
  migrations.
- **Two-client MinIO.** Internal endpoint for puts; public endpoint
  for signed GETs — swap the public one for a CDN in production.
- **Redis = live state, Postgres = durable truth.** Presence,
  typing, voice participants live in Redis and cease to exist when
  the owning session ends.
- **Authz via OpenFGA.** Services never read `guild_members` to
  decide policy — they call `authz.Can…`.

## What's intentionally *not* here

- Frontend details — clients implement their own state management.
- Kubernetes manifests — we deploy as plain binaries; bring your
  own orchestrator.
- Redis / Postgres HA — use a managed service.
- Kafka / JetStream — we don't need durable event logs today.
