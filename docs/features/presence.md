# Presence

User online / idle / dnd / offline state. The *live* state lives in
Redis and is owned entirely by the `StreamFriendActivity` lifetime —
connect writes the user's DB intent into Redis; disconnect writes
offline.

## Package

[internal/features/presence](../../internal/features/presence)

## gRPC surface (proto: [presence/v1/presence.proto](../../internal/features/presence/proto/presence/v1/presence.proto))

| RPC | What it does |
|---|---|
| `UpdatePresence` | Set status + custom status (writes Redis, publishes event) |
| `GetPresence` | Read own presence |
| `GetBulkPresence` | Read presence for many users at once (friend list UIs) |

The client-facing gRPC exposes explicit status changes. The
**automatic** online/offline tracking doesn't use an RPC — it's
wired to the stream lifecycle.

## Data it owns

- `presence:<userID>` Redis hash: `{status, custom_status, last_seen}`.
  No TTL — the stream lifecycle manages its existence.

## Enum mappings

Three enums are involved; they all represent the same set of
statuses (`online / idle / dnd / offline`) but live in different
namespaces for encapsulation:

| Enum | Where | Purpose |
|---|---|---|
| `presencev1.Status` | presence proto | RPC request/response |
| Hash value `string` | Redis | persisted column |
| `streamv1.PresenceStatus` | stream proto | wire-level event enum |

`statusToString` and `statusToStream` maps (in service.go) translate
between them.

## `UpdatePresence` flow

```
client → UpdatePresence(status, customStatus)
  ├─ HSET presence:<me> { status, custom_status, last_seen: now }
  └─ publishPresence → FriendActivityEvent{PRESENCE_UPDATE} on friend.<me>.activity
```

The event goes to the user's own activity subject — their friends'
`StreamFriendActivity` subscriptions include that subject, so they
see it.

## Lifecycle hooks — `OnUserConnect` / `OnUserDisconnect`

These are the hooks the stream handler calls when
`StreamFriendActivity` opens/closes.

### OnUserConnect

```go
func (s *Service) OnUserConnect(ctx context.Context, userID string) {
    status, custom := s.resolver.GetStatusIntent(ctx, userID)
    st, ok := stringToStatus[status]
    if !ok { st = Status_STATUS_ONLINE }
    s.UpdatePresence(ctx, userID, st, custom)
}
```

Reads the DB intent (via `UserInfoResolver.GetStatusIntent`, which is
`user.Service`) and writes it to Redis + publishes. A fresh account
with no chosen status still shows as ONLINE so friends see them.

### OnUserDisconnect

```go
func (s *Service) OnUserDisconnect(ctx context.Context, userID string) {
    s.SetOffline(ctx, userID)
}
```

Always OFFLINE. We do NOT persist this back to `users.status` — the
DB stores the user's *preference*, not their current connectivity.

## `GetLiveStatus` — the read path for enrichment

```go
func (s *Service) GetLiveStatus(ctx, userID) (status, customStatus string) {
    vals, _ := s.redis.HGetAll(presenceKeyPrefix + userID).Result()
    if len(vals) == 0 {
        return "offline", ""
    }
    return vals["status"], vals["custom_status"]
}
```

Used by the **user** feature's `friendPayload` so a friend_request or
friend_accepted event stamped with the sender's profile shows their
*live* status, not the stale DB intent. If a user is not in Redis
at all (never opened their app), the call returns `"offline"` so a
stranger looking at their profile via a pending friend request sees
offline rather than their chosen preference.

## Interactions

| Feature | Why it talks to presence |
|---|---|
| `stream` | `OnUserConnect` / `OnUserDisconnect` around `StreamFriendActivity` |
| `auth` | `Logout` → `SetOffline` (explicit logout ≠ stream close) |
| `user` | `friendPayload` calls `GetLiveStatus` to enrich friend events |

Wiring lives in [app/services.go](../../internal/app/services.go):

```go
authHandler.SetPresenceUpdater(presenceSvc)
streamHandler.SetPresenceController(presenceSvc)
userSvc.SetPresenceReader(presenceSvc)
```

## Event catalogue

All presence-related events carry the `streamv1.FriendActivityEvent`
envelope on `friend.<userID>.activity`:

| Trigger | `FriendEventType` | `PresenceStatus` | Who receives |
|---|---|---|---|
| Stream connect | `PRESENCE_UPDATE` | user's intent | user + friends |
| Stream disconnect / logout | `PRESENCE_UPDATE` | `OFFLINE` | user + friends |
| Manual `UpdatePresence` | `PRESENCE_UPDATE` | requested | user + friends |

## Failure modes

- **Redis down** — `OnUserConnect` fails silently (error is
  suppressed) and no event is published. Friends see the user as
  whatever state they were last known to be in. Recovery on the next
  event.
- **User has two sessions** — each open stream re-runs
  `OnUserConnect`, which is idempotent. The `OnUserDisconnect` fires
  per stream too, so closing ONE session writes OFFLINE even though
  the other is still open. **Known limitation**: multi-device users
  flicker. Fix would be a refcount in Redis; deferred.
