# Voice

Voice channel state, DM calls, and the LiveKit webhook bridge. The
**voice state** is the source of truth, not LiveKit — LiveKit only
knows WebRTC-level reality (who has a mic track); mute / deafen /
screen-share intent lives in our Redis hash.

## Package

[internal/features/voice](../../internal/features/voice)

Files:
- [service.go](../../internal/features/voice/service.go) — RPCs, LiveKit token mint
- [state.go](../../internal/features/voice/state.go) — Redis participant hash + toggles
- [webhook.go](../../internal/features/voice/webhook.go) — LiveKit event receiver

## gRPC surface (proto: [voice/v1/voice.proto](../../internal/features/voice/proto/voice/v1/voice.proto))

### Guild voice

| RPC | What it does |
|---|---|
| `JoinChannel` | Create LiveKit room, mint token, seed Redis state |
| `LeaveChannel` | Kick from LiveKit, drop Redis state |
| `GetChannelParticipants` | List live LiveKit participants |
| `Mute` / `Unmute` | Toggle `self_mute` in Redis + broadcast |
| `Deafen` / `Undeafen` | Toggle `self_deaf` (implies mute) |
| `EnableVideo` / `DisableVideo` | Toggle `video` intent |
| `StartScreenShare` / `StopScreenShare` | Toggle `screen_share` intent |

### DM calls

| RPC | What it does |
|---|---|
| `StartDMCall` | Create room, mint token, ring recipients (`call_incoming`) |
| `JoinDMCall` | Recipient joins (`call_accepted`) |
| `RejectDMCall` | Recipient declines (`call_rejected`) |
| `LeaveDMCall` | Hang up (`participant_left`, `call_ended` if empty) |

## Data it owns

- `voice:<channelID>` Redis hash — map userID → JSON `ParticipantState`
- `voice:<channelID>:active_since` Redis key — first-joiner timestamp
- LiveKit rooms — named by channel id, metadata JSON = `{guildId, channelId}`

## `ParticipantState`

```go
type ParticipantState struct {
    UserID, Username, DisplayName, AvatarURL, GuildID, ChannelID string
    SelfMute, SelfDeaf, Video, ScreenShare bool
}
```

Stored in Redis as JSON per user. Authoritative for the mute/deafen/
video/screen-share flags — LiveKit doesn't know about `self_deaf`.

## Room lifecycle

### First joiner

```
JoinChannel(userID, guildID, channelID)
  ├─ authz.CanViewChannel(userID, channelID)
  ├─ LiveKit CreateRoom (idempotent; metadata = {guildId, channelId})
  ├─ mint LiveKit JWT token (identity = userID, metadata = profile JSON)
  ├─ SetParticipant → HSET voice:<c> <u> <json>
  ├─ EnsureActiveSince → SETNX voice:<c>:active_since now()
  └─ broadcastChannelState → VOICE_STATE_UPDATE{STATE_SYNC} per participant
```

### State toggles

All of `Mute/Unmute/Deafen/Undeafen/Enable/DisableVideo/Start/StopScreenShare`
go through the shared `toggleField`:

```go
toggleField(ctx, userID, channelID, func(ps *ParticipantState) { ps.SelfMute = true })
  → load participant
  → mutate in place
  → SetParticipant (HSET)
  → broadcastParticipant → VoiceStateEvent{STATE_UPDATE}
  → return GetChannelState (full list for the RPC response)
```

The `RoomActiveSince` field on every `VoiceStateEvent` carries the
first-joiner timestamp so **late subscribers** can render an accurate
"room has been live for 12 minutes" timer without a separate RPC.

### Leave

```
LeaveChannel(userID, channelID)
  ├─ LiveKit RemoveParticipant
  ├─ RemoveParticipant
  │    ├─ read guild_id from Redis (before HDEL)
  │    ├─ HDEL voice:<c> <u>
  │    ├─ publish VoiceStateEvent{LEAVE} with RoomActiveSince
  │    └─ if HLEN voice:<c> == 0 → DEL voice:<c> + DEL active_since
  └─ broadcastChannelState (updated list)
```

### Room teardown

When LiveKit sees the room empty for `EmptyTimeout` (300s for guild
rooms, 60s for DM calls), it fires `room_finished`. The webhook:

```
room_finished → ClearChannel + ClearActiveSince + VoiceStateEvent{ROOM_FINISHED}
```

## Webhook — LiveKit → NATS

[webhook.go](../../internal/features/voice/webhook.go) receives LiveKit
events over HTTP at `POST :9100/livekit/webhook`. The
`SimpleKeyProvider` verifies the signature with `LIVEKIT_API_SECRET`.

```
handleEvent
  ├─ resolveRoomMeta (uses per-channel cache because track events
  │   don't carry room metadata)
  └─ switch event.GetEvent():
        room_started        → VoiceStateEvent{ROOM_STARTED}
        room_finished       → clear Redis + ROOM_FINISHED
        participant_joined  → VoiceStateEvent{JOIN}
        participant_left    → RemoveParticipant + LEAVE + broadcastChannelState
        track_published     → publishTrackEvent(published=true)  → VoiceStateEvent{TRACK_UPDATE}
        track_unpublished   → publishTrackEvent(published=false) → TRACK_UPDATE
        participant_active  → publishParticipantState → VoiceStateEvent{STATE_SYNC}
```

### Why merge Redis state in `publishTrackEvent`

LiveKit's `track_published(CAMERA)` webhook only knows about the camera
track. If we published a raw event with `video=true` and nothing else,
we'd clobber the user's existing `screen_share=true` intent. So the
handler reads Redis state first and merges the track flip into the
full picture before publishing.

## DM calls

A DM call is a LiveKit room named after the DM channel id. Signalling
rides a separate subject (`dm.<userID>.call`) because a ringing
recipient isn't yet in the channel — they can't subscribe to
`guild.<g>.channel.voice.*`.

Event types on this subject use the `DmCallType` enum:

- `DM_CALL_INCOMING` — caller has initiated; ring.
- `DM_CALL_ACCEPTED` — someone joined.
- `DM_CALL_REJECTED` — someone declined.
- `DM_CALL_ENDED` — last participant left; dismiss UI.
- `DM_CALL_PARTICIPANT_LEFT` — mid-call hang-up.

## Initial snapshot for `StreamVoiceState`

When a client opens `StreamVoiceState`, the handler first emits a
snapshot (built by [VoiceSnapshot](../../internal/features/voice/service.go#L202))
for every voice channel the user's guilds contain — one
`VoiceStateEvent{STATE_SYNC}` per live participant. The priority is:

1. `LiveKit.ListParticipants` — who is actually *connected*.
2. Redis `voice:<c>` — their mute/deaf/video/screen flags.
3. If LiveKit returns empty, fall back to Redis only (covers the
   window where a user has called `JoinChannel` but WebRTC is still
   negotiating).

After the snapshot, the handler subscribes to the wildcard
`guild.<g>.channel.voice.*` and forwards live events.

## Failure modes

- **LiveKit down** → `JoinChannel` returns `ErrLiveKitUnavailable`.
  No partial state written.
- **Redis down** during a toggle → toggle fails, broadcast skipped.
  Redis is effectively required.
- **Webhook delivery fails** → LiveKit retries; for transient blips
  state eventually converges. For persistent failure, `room_finished`
  never fires — a janitor job should sweep `voice:*` hashes with no
  active LiveKit room (TODO).
