# Sync

Bulk state fetches used to hydrate a freshly-launched client without
tens of individual RPCs. Nothing here writes — it's pure read
aggregation on top of existing repositories.

## Package

[internal/features/sync](../../internal/features/sync)

## gRPC surface (proto: [sync/v1/sync.proto](../../internal/features/sync/proto/sync/v1/sync.proto))

| RPC | What it does |
|---|---|
| `InitialSync` | Everything a client needs on app-open: guilds + channels + DM list + friends + unread state |
| `ResyncChannel` | Unread message count + latest read marker for a single channel |
| `ResyncGuild` | Channel list + member count + role list for one guild (pull on guild-open) |

## Why these exist

Without sync, the client would issue dozens of calls to render the
home screen:

- `ListUserGuilds`
- For each guild: `ListGuildChannels`, `ListRoles`, `CountMembers`
- `ListDMChannels`
- `ListFriends`
- Per channel: read-state from Scylla

Each of those is a separate round trip. `InitialSync` fans the work
out server-side in parallel goroutines and returns one protobuf, so
app-launch is ~1 RTT.

## Implementation pattern

```go
func (s *Service) InitialSync(ctx, userID) (*InitialSyncResponse, error) {
    var (
        guilds   []*Guild
        dms      []*DMChannel
        friends  []*Friend
        unread   map[string]int32
    )
    g, gctx := errgroup.WithContext(ctx)
    g.Go(func() error { … fetch guilds … })
    g.Go(func() error { … fetch DMs + members … })
    g.Go(func() error { … fetch friends + live presence … })
    g.Go(func() error { … fetch read_states from Scylla … })
    if err := g.Wait(); err != nil { return nil, err }
    return &InitialSyncResponse{…}, nil
}
```

Each goroutine talks to a different store (Postgres for guilds /
friends, Scylla for read states). They're independent so running in
parallel shaves latency.

## Read-state aggregation

`unread` is computed as `message_count_since_last_read_id`. Scylla's
partition key (`channel_id`) makes this cheap — one range read per
channel to count messages newer than the last read marker. In
practice the client shows a badge at ∞ once counts get large, so we
cap at 99+ server-side to avoid wasting cycles.

## Friend hydration

`ListFriends` alone returns UUIDs. `InitialSync` joins in:

- Username, display name, avatar URL — from `users`.
- **Live status** — from `presence:<userID>` Redis hash (`GetBulkPresence`).
- Mutual guilds — from `guild_members`.

So the sidebar renders without a second round trip.

## When clients call sync

- **App launch / foreground resume after > 5 min** — `InitialSync`.
- **Open a guild** — `ResyncGuild` (pull fresh channel / role list).
- **Open a channel** — `ResyncChannel` (re-check unread) + `ListMessages`.

For realtime updates after sync, clients rely on
[StreamService](./stream.md) — sync is the cold start, stream is the
steady state.

## Failure modes

- **One of the parallel fetches fails** — `errgroup` cancels the rest
  and returns the error. Client falls back to on-demand RPCs. We
  don't try to deliver a partial response because the UI treats
  missing fields as "present but empty", which is worse than an
  error.
- **Redis down** during friend hydration → presence fields come back
  blank; client treats as `OFFLINE`. Acceptable degradation.
