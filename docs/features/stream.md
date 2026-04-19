# Stream

The outside world's window into NATS. Every "what's happening right
now" feature in the app is exposed as one server-streaming RPC that
maps to a NATS subject (or a set of subjects resolved per-user).

## Package

[internal/features/stream](../../internal/features/stream)

## gRPC surface (proto: [stream/v1/stream.proto](../../internal/features/stream/proto/stream/v1/stream.proto))

| RPC | Event proto | Subjects subscribed |
|---|---|---|
| `StreamDmChat` | `DmChatEvent` | `dm.<me>.message.*` |
| `StreamDmChannels` | `DmChannelEvent` | `dm.<me>.channels` |
| `StreamDmCalls` | `DmCallEvent` | `dm.<me>.call` |
| `StreamTextChannels` | `TextChannelEvent` | `guild.<g>.channel.message.*` × `g ∈ my guilds` |
| `StreamVoiceChat` | `VoiceChatEvent` | `guild.<g>.channel.voicechat.*` × `g` |
| `StreamGuildEvents` | `GuildEvent` | `guild.<g>.events` × `g` |
| `StreamVoiceState` | `VoiceStateEvent` | snapshot + `guild.<g>.channel.voice.*` × `g` |
| `StreamTyping` | `TypingEvent` | `dm.<me>.typing.*` + `guild.<g>.channel.typing.*` × `g` |
| `StreamFriendActivity` | `FriendActivityEvent` | `friend.<me>.activity` + `friend.<f>.activity` × friends |

## The generic subscriber

[handler.go](../../internal/features/stream/handler.go) has one generic
helper that backs almost every RPC:

```go
func streamFromSubjects[T any](
    h *Handler, ctx context.Context,
    subjects []string, send func(*T) error,
) error {
    multi, _ := h.nats.SubscribeMulti(subjects)
    defer multi.Unsubscribe()
    for {
        select {
        case <-ctx.Done(): return nil
        case msg := <-multi.Ch:
            var evt T
            if json.Unmarshal(msg.Data, &evt) == nil {
                send(&evt)
            }
        }
    }
}
```

Cancel semantics are simple — `stream.Context()` cancels when the
client disconnects; the `<-ctx.Done()` drops the subscription,
cleans up NATS and the goroutine exits.

## Resolver — what does "my guilds / my friends" mean?

`UserDataResolver` (implemented by
[stream.Resolver](../../internal/features/stream/resolver.go)) pulls:

- `GetUserGuildIDs(ctx, userID)` — active guild memberships (not
  left, not kicked, not banned).
- `GetUserFriendIDs(ctx, userID)` — accepted friendships in both
  directions.

Both hit Postgres — a single query each, cached only by Postgres's
shared buffer. If this turns hot, cache in Redis with a short TTL +
invalidate on guild-join / friend-accept events.

## Initial snapshot for voice

`StreamVoiceState` is unusual: before subscribing to NATS, it walks
every guild's voice channels and emits a `STATE_SYNC` event per live
participant. The snapshot source is
[`voice.VoiceSnapshot`](../../internal/features/voice/service.go) —
LiveKit for "who's connected" plus Redis for "what state they're in".
That's why reconnecting to the app doesn't show an empty voice
channel for a few seconds.

## Lifecycle hook — presence

`StreamFriendActivity` wires a two-part presence hook into its
lifetime:

```go
if h.presence != nil {
    h.presence.OnUserConnect(stream.Context(), userID)
    defer func() {
        // fresh ctx: stream.Context() is already Done by now
        h.presence.OnUserDisconnect(context.Background(), userID)
    }()
}
```

- **On connect**: read the user's status intent from the DB, write to
  Redis `presence:<userID>`, publish `FRIEND_EVENT_PRESENCE_UPDATE`.
- **On disconnect**: write OFFLINE, publish `FRIEND_EVENT_PRESENCE_UPDATE`.

This is how friends see accurate "online" / "offline" state without
explicit heartbeats. See [presence.md](./presence.md) and
[user.md](./user.md) for the resolver side.

## Wire format quirk

Publishers marshal proto structs with plain `encoding/json`, taking
advantage of the generated `json:"…"` tags. Proto enum fields (type /
event / action / status / track_type) travel as **integers** because
that's how `encoding/json` renders proto int32 aliases. Clients must
decode them into the corresponding enum value.

This is intentional: the alternative (protojson / string enums)
doubles event size and couples the wire to the Go proto names.

## Why a gRPC-stream-per-topic, not a single bidi stream?

- Back-pressure is per-topic — a slow `StreamVoiceState` doesn't
  block message delivery.
- Clients pick and choose — a mobile client might skip
  `StreamTyping` to save battery.
- Reconnect is independent — losing the voice stream doesn't drop
  DM delivery.
- gRPC server-streaming is the simplest primitive that gives us
  backpressure + cancellation for free.

## Failure modes

- **NATS down** → `SubscribeMulti` returns an error → RPC returns
  `Internal`. Client retries with backoff; there's no stale data to
  reconcile because we never cached anything.
- **Slow client** → the per-subscription channel buffer (256) fills
  up; new NATS messages are dropped silently (the `select` default).
  Clients must tolerate gaps — in practice the consistent snapshot
  pattern (every event carries full state) means the next message
  heals them.
- **Stream close mid-send** — `ctx.Done()` wins, the generic helper
  returns `nil`, NATS subscription is cleaned by `defer`.
