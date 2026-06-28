# User

User profile, friend graph, block list, avatar upload and the
enrichment helper every friend/presence event goes through.

## Package

[internal/features/user](../../internal/features/user)

## gRPC surface (proto: [user/v1/user.proto](../../internal/features/user/proto/user/v1/user.proto))

| RPC | What it does |
|---|---|
| `GetUser` | Fetch profile by id |
| `GetUserByUsername` | Profile by `name#1234` handle |
| `UpdateUsername` | One-shot set of the caller's username (fails if already set) |
| `UpdateUser` | Update display name, status, custom status, background color |
| `UpdateStatus` | Dedicated status-only update that emits `PRESENCE_UPDATE` |
| `SearchUsers` | Substring search with pagination |
| `UploadAvatar` | Upload image bytes to MinIO, persist object key |
| `SendFriendRequest` / `AcceptFriendRequest` / `DeclineFriendRequest` / `CancelFriendRequest` | Friend graph lifecycle |
| `RemoveFriend` / `ListFriends` / `ListPendingRequests` | Friend graph reads |
| `BlockUser` / `UnblockUser` / `ListBlocks` / `IsBlocked` | Block list |

## Data it owns

- `users` (Postgres)
- `friendships` (Postgres) — directed edges
- MinIO `users/<id>/avatar_…`

## Friend graph state machine

Each `friendships` row represents a directed edge from `user_id` to
`friend_id`. Status:

```
            SendFriendRequest
user ────────────────────────────► pending
                                     │
     AcceptFriendRequest (by target)│
                                     ▼
                                 accepted
     BlockUser / RemoveFriend      │
                                     ▼
                         blocked / deleted
```

- `SendFriendRequest` creates a `pending` row unless an existing row
  says `accepted` / `pending` / `blocked`.
- `AcceptFriendRequest` requires the current user is the recipient
  (friend_id), flips to `accepted`, and **auto-creates a DM channel**
  via the injected `DMOpener` interface (implemented by
  `channel.DMResolver`).
- `CancelFriendRequest` is the sender-side equivalent of decline — it
  only works if the caller is the sender and the row is still pending.

## Friend activity events

Every mutation publishes a `streamv1.FriendActivityEvent` on the
**recipient's** subject `friend.<recipientID>.activity` — the sender's
other devices get the event too when they subscribe to their own
subject.

| RPC | Event | Receiver subject |
|---|---|---|
| `SendFriendRequest` | `FRIEND_EVENT_REQUEST` | target |
| `AcceptFriendRequest` | `FRIEND_EVENT_ACCEPTED` | both (carries DM channel id) |
| `DeclineFriendRequest` | `FRIEND_EVENT_DECLINED` | requester |
| `CancelFriendRequest` | `FRIEND_EVENT_REQUEST_CANCELLED` | target |
| `RemoveFriend` | `FRIEND_EVENT_REMOVED` | the removed friend |
| `UpdateUser` | `FRIEND_EVENT_PROFILE_UPDATE` | self (broadcast to friends via shared subject) |
| `UpdateStatus` | `FRIEND_EVENT_PRESENCE_UPDATE` | self |
| `UploadAvatar` | `FRIEND_EVENT_PROFILE_UPDATE` | self |

## Enrichment — `friendPayload`

`friendPayload(ctx, actorID)` returns a `*streamv1.FriendActivityEvent`
with the actor's profile baked in so the recipient can render it
without a follow-up `GetUser` call.

Critically, it consults the wired `PresenceReader` to stamp the
**live** status (from Redis) rather than the DB-persisted intent:

```go
if s.presence != nil {
    if live, custom := s.presence.GetLiveStatus(ctx, pgIDToStr(u.ID)); live != "" {
        statusStr = live
        customStr = custom
    }
}
```

That's why a friend_request event to Bob will say
`status = OFFLINE` when the sender has their app closed, instead of
the DB's "online" intent.

## Status intent vs live status

- **Intent** (`users.status`) — the user's *choice*: online / idle /
  dnd / offline. Survives restarts.
- **Live** (`presence:<userID>` hash) — their *actual* connectivity.
  Set when `StreamFriendActivity` opens; cleared to `offline` when it
  closes.

`GetStatusIntent(ctx, userID)` reads the intent; `GetLiveStatus`
(owned by the presence feature) reads the live value. The stream
handler restores intent → live on connect and writes offline → live
on disconnect.

## Blocks

Block rows live in `friendships` with `status = 'blocked'`. The
`BlockChecker` interface (implemented by `user.NewBlockChecker`) is
injected into the `message` service so `SendDirectMessage` can refuse
to deliver to a blocked user.

## Avatar upload

1. `UploadAvatar(userID, filename, contentType, data)` validates size
   ≤ 5 MB and content type ∈ {jpeg, png, gif, webp}.
2. Writes to MinIO at `users/<userID>/avatar_<UUID>_<filename>`.
3. `UPDATE users SET avatar_url = <bucket_key>`.
4. Publishes `FRIEND_EVENT_PROFILE_UPDATE` with the *presigned* URL
   (7-day TTL) — clients cache the URL not the key.

## Dependencies injected

| Needed | Interface | Impl |
|---|---|---|
| Create DM on friend accept | `DMOpener` | `channel.DMResolver` |
| Presigned GETs for avatars | `*minio.Client` signer | `infra.MinIOSigner` |
| Live status for enrichment | `PresenceReader` | `presence.Service` |

## Failure modes

- **MinIO put succeeds but Postgres update fails** → orphan object.
  Periodic sweeper (not written) should purge objects without a
  `media_files` row. Avatars use a random UUID in the path so a retry
  never overwrites.
- **Presence reader returns empty** → fall back to DB intent. A new
  user who has never opened the stream still shows as their chosen
  status rather than blank.
- **Block a friend** → the friendship row flips to `blocked`, which
  both removes them from friend listings and short-circuits DM sends.
