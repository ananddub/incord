# NATS Subject Catalog

All realtime fan-out goes through NATS. One feature writes to a
well-known subject; `StreamService` is the sole subscriber that
reaches the outside world (clients talk gRPC, not NATS).

Subject builders and wildcard helpers live in
[internal/shared/realtime/nats.go](../internal/shared/realtime/nats.go).

## Subject tree

```
guild.<guildID>.channel.message.<channelID>     text message in guild channel
guild.<guildID>.channel.typing.<channelID>      typing in guild channel
guild.<guildID>.channel.voice.<channelID>       voice state in voice channel
guild.<guildID>.channel.voicechat.<channelID>   text chat inside a voice channel
guild.<guildID>.events                          guild-level (member/role/channel/…)

dm.<userID>.message.<channelID>                 DM message (per-recipient fanout)
dm.<userID>.typing.<channelID>                  DM typing indicator
dm.<userID>.channels                            DM channel lifecycle
dm.<userID>.call                                DM call signalling

friend.<userID>.activity                        presence, profile, friend events
```

### Wildcards used by StreamService

```
guild.<guildID>.channel.message.*      every text channel in a guild
guild.<guildID>.channel.typing.*       every typing signal in a guild
guild.<guildID>.channel.voice.*        every voice channel
guild.<guildID>.channel.voicechat.*    every in-voice text chat
dm.<userID>.message.*                  all DM messages for a user
dm.<userID>.typing.*                   all DM typing for a user
```

## Publishers

Every publish is a typed proto — never `map[string]any`.

| Subject | Publisher | Payload proto |
|---|---|---|
| `guild.<g>.channel.message.<c>` | `message.Service` (send/edit/delete/pin/react/read-receipt) | `streamv1.TextChannelEvent` |
| `guild.<g>.channel.typing.<c>` | `message.Service.StartTyping` | `streamv1.TypingEvent` |
| `guild.<g>.channel.voice.<c>` | `voice.Service` (state toggles) + `voice.WebhookHandler` (LiveKit events) + `voice.state.RemoveParticipant` | `streamv1.VoiceStateEvent` |
| `guild.<g>.channel.voicechat.<c>` | reserved (currently unused — voice chat shares `TextChannelEvent` routing) | `streamv1.VoiceChatEvent` |
| `guild.<g>.events` | `guild.Service` (every mutation via `publishGuildEvent`) + `channel.Service` (CHANNEL_CREATE/UPDATE/DELETE) | `streamv1.GuildEvent` |
| `dm.<u>.message.<c>` | `message.Service` (fans out to every DM member) | `streamv1.TextChannelEvent` (DmChatEvent JSON-compatible) |
| `dm.<u>.typing.<c>` | `message.Service.StartTyping` | `streamv1.TypingEvent` |
| `dm.<u>.channels` | `channel.Service.publishDMChannelEvent` | `streamv1.DmChannelEvent` |
| `dm.<u>.call` | `voice.Service.Start/Join/Reject/Leave DMCall` + `webhook.publish` for non-guild rooms | `streamv1.DmCallEvent` (or `VoiceStateEvent` for webhook track events) |
| `friend.<u>.activity` | `user.Service.publishFriendActivity` + `presence.Service.publishPresence` | `streamv1.FriendActivityEvent` |

## Subscribers (StreamService)

[internal/features/stream/handler.go](../internal/features/stream/handler.go)
maps one gRPC server-stream RPC to a set of NATS subjects based on
who the caller is:

| RPC | Subjects subscribed (per caller) |
|---|---|
| `StreamDmChat` | `dm.<me>.message.*` |
| `StreamDmChannels` | `dm.<me>.channels` |
| `StreamDmCalls` | `dm.<me>.call` |
| `StreamTextChannels` | `guild.<g>.channel.message.*` for every `g` I'm in |
| `StreamVoiceChat` | `guild.<g>.channel.voicechat.*` for every `g` I'm in |
| `StreamGuildEvents` | `guild.<g>.events` for every `g` I'm in |
| `StreamVoiceState` | `guild.<g>.channel.voice.*` for every `g` I'm in (plus an initial snapshot from Redis+LiveKit) |
| `StreamTyping` | `dm.<me>.typing.*` and `guild.<g>.channel.typing.*` for every `g` |
| `StreamFriendActivity` | `friend.<me>.activity` and `friend.<friend>.activity` for every friend |

Guild membership is resolved by the `UserDataResolver` interface
([internal/features/stream/resolver.go](../internal/features/stream/resolver.go))
which reads Postgres.

## Payload / wire contract

Publishers marshal proto structs with `encoding/json` (the generated
types carry JSON tags). The `StreamService` handler JSON-unmarshals
into a typed proto in a generic `streamFromSubjects[T]` helper and
calls `stream.Send(&evt)`. No `map[string]any` in between.

**Proto3 enum quirk** — enum fields (`type`, `event`, `action`,
`status`, `track_type`) travel as integers over JSON (their proto
tag number). Clients must decode them into the matching enum value;
names are compiled into `stream.proto`'s:

- `ChatEventType` · `ChannelLifecycleType` · `DmCallType`
- `VoiceEvent` · `VoiceAction` · `VoiceTrackType`
- `FriendEventType` · `PresenceStatus` · `GuildEventType`

## Event lifecycle examples

### A guild message

```
SendMessage RPC
  → Scylla write (messages + attachments)
  → build *streamv1.TextChannelEvent with type=CREATE
  → publish on guild.<g>.channel.message.<c>
  → every StreamTextChannels subscriber for guild g gets it
```

### A user comes online

```
StreamFriendActivity RPC opens
  → handler calls PresenceController.OnUserConnect
  → presence reads users.status from Postgres (intent)
  → writes to Redis presence:<me>
  → publishes FriendActivityEvent{PRESENCE_UPDATE, online} on friend.<me>.activity
  → every friend's StreamFriendActivity gets the update
  (… stream closes …)
  → deferred OnUserDisconnect
  → writes OFFLINE to Redis
  → publishes FriendActivityEvent{PRESENCE_UPDATE, offline}
```

### A voice room activates

```
First user JoinChannel
  → LiveKit CreateRoom (idempotent)
  → voice.SetParticipant → Redis voice:<c>
  → voice.EnsureActiveSince → Redis voice:<c>:active_since (SETNX)
  → broadcastChannelState → per-user VoiceStateEvent{STATE_SYNC}
LiveKit fires participant_joined webhook
  → WebhookHandler.publish → VoiceStateEvent{JOIN}
(user mutes)
  → voice.Mute → toggleField → broadcastParticipant → VoiceStateEvent{STATE_UPDATE}
(last user leaves)
  → RemoveParticipant → LEAVE event + ClearActiveSince
  → LiveKit room_finished webhook → ROOM_FINISHED event
```

## Why NATS, not Kafka / Redis Streams?

- Fire-and-forget fan-out with many wildcard subscriptions is
  NATS's sweet spot; we don't need durable replay.
- `StreamService` gives us the durability story — reconnecting
  clients run a snapshot RPC (e.g. `ListMessages`, `GetVoiceState`)
  and then resubscribe; we never need NATS JetStream.
- NATS cluster + basic auth is trivial to operate vs Kafka.

If we ever need durable + replayable event logs (audit trail replay,
feature-flag experiments), add a single JetStream consumer that
copies selected subjects into a stream — no code changes in publishers.
