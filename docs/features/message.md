# Message

Chat messages, edits, deletes, pins, reactions, read receipts, typing
indicators, threads and forwards. The hot path of the app.

## Package

[internal/features/message](../../internal/features/message)

## gRPC surface (proto: [message/v1/message.proto](../../internal/features/message/proto/message/v1/message.proto))

| RPC | What it does |
|---|---|
| `SendMessage` | Post to a channel — supports reply, attachments, forward, mentions |
| `EditMessage` | Update content, record edit history |
| `DeleteMessage` | Soft-delete (cascades to child replies) |
| `GetMessage` / `ListMessages` | Reads (pagination via before/after id) |
| `PinMessage` / `UnpinMessage` | Toggle `pinned` |
| `AddReaction` / `RemoveReaction` / `GetReactions` | Reaction toggles |
| `AckMessage` | Mark latest read; drives unread badges |
| `StartTyping` | 8s typing indicator |
| `SendDirectMessage` | Auto-create / resolve DM, then SendMessage |
| `GetMessageEditHistory` | Immutable edit log |

## Data it owns

- `messages`, `message_attachments`, `message_reactions`,
  `message_edit_history`, `read_states` (Scylla)
- `typing:<channelID>` Redis key (8s TTL)

## The publish envelope

All chat mutations go through two helpers:

### `buildMessageEvent`

Builds a rich `*streamv1.TextChannelEvent` so subscribers don't need a
follow-up `GetMessage`. Carries:

- `type` (ChatEventType enum)
- ids, author, sender, content
- `reactions` (full per-emoji snapshot, not a delta)
- `attachments`
- `reply_to_id`, `forwarded_from`, `mention_user_ids`
- `edited_at`, `created_at`, `pinned`, `deleted`

### `publishChannelEvent`

Routes the typed payload to the right subject:

```go
if guildID != "" {
    nats.Publish(GuildChannelMessage(guildID, channelID), evt)
} else {
    // DMs fan out to every member's personal subject so all their
    // devices see it — including the sender's other sessions.
    for _, memberID := range dmChannelList.GetDMChannelMemberIDs(ctx, channelID) {
        nats.Publish(DmMessage(memberID, channelID), evt)
    }
}
```

The same `TextChannelEvent` JSON round-trips cleanly into `DmChatEvent`
on the DM stream path because the JSON field names line up — the
extra `guild_id` is simply ignored.

## ChatEventType catalogue

Every mutation uses a typed enum, no raw strings:

| ChatEventType | Emitted by |
|---|---|
| `CHAT_EVENT_CREATE` | `SendMessage` |
| `CHAT_EVENT_UPDATE` | `EditMessage` |
| `CHAT_EVENT_DELETE` | `DeleteMessage` |
| `CHAT_EVENT_PIN` / `CHAT_EVENT_UNPIN` | `setPinned` |
| `CHAT_EVENT_REACTION_ADD` / `CHAT_EVENT_REACTION_REMOVE` | `toggleReaction` |
| `CHAT_EVENT_READ_RECEIPT` | `AckMessage` |

## Why rich snapshots, not deltas

A reaction add emits the **full** reactions list, not a single delta.
Reason:

- Clients replace local state wholesale → correctness without CRDT
  bookkeeping.
- Missed / out-of-order events can't corrupt counts — the latest
  event is always authoritative.
- Cost is a Scylla read per event, which is one partition hit —
  cheap.

This is what makes multi-device sync "just work": every subscribed
device (including the reactor's own other sessions) receives an
authoritative state snapshot on every mutation.

## Mentions

`SendMessage` accepts an explicit `mention_user_ids` list — we do NOT
parse the content server-side. The client is responsible for the
actual @-detection in the UI; the server trusts and dedupes the list.

For each mentioned user:

```go
repo.IncrementMentionCount(ctx, mentionedUserID, channelID)
```

This bumps `read_states.mention_count` so unread badges can show
"@-mentioned" separately.

## Forwards

`ForwardSource{ChannelID, MessageID}` is optional on `SendMessage`.
When present:

1. Validate source exists (`GetMessage`).
2. Copy source's content if caller didn't supply their own.
3. Copy source's attachments so the forward is self-contained even if
   the original gets deleted.
4. Stamp `forwarded_from_*` on the new row for UI rendering.

## Edits

`EditMessage` writes the **old** content to `message_edit_history`
before overwriting. The published `UPDATE` event carries the new
content; clients fetch `GetMessageEditHistory` if the user opens the
edit timeline.

## Deletes

`DeleteMessage` is a soft delete. Cascade: all child replies to the
tombstoned message are also soft-deleted
(`CascadeDeleteChildren`). The published `DELETE` event has
`deleted = true` and empty content; clients drop the message bubble.

## Typing

```go
redis.Set("typing:<channelID>", userID, 8*time.Second)
```

Then publish a `streamv1.TypingEvent` on either
`guild.<g>.channel.typing.<c>` or `dm.<userID>.typing.<c>` for each
DM member (except the typist themselves). Clients render a "user is
typing…" badge that disappears 8s after the last event.

## Read receipts

`AckMessage` updates `read_states(user_id, channel_id)` and
publishes a `TextChannelEvent` with `Type = CHAT_EVENT_READ_RECEIPT`
so other members' clients update "seen by X" indicators.

## Dependencies injected

| Need | Interface | Impl |
|---|---|---|
| Resolve DM channel on `SendDirectMessage` | `DMChannelResolver` | `channel.DMResolver` |
| Block check on DM | `BlockChecker` | `user.BlockChecker` |
| Fan out DM events | `DMChannelLister` | `channel.DMChannelMembersResolver` |
| Resolve attachments on send | `MediaResolver` | `media.Service` |
| Guild lookup for a channel | `GuildResolver` | `channel.GuildResolver` |

## Failure modes

- **Scylla write succeeds, NATS publish fails** → the message exists
  but clients don't see it until they reopen the channel (next
  `ListMessages` returns it). Acceptable loss — NATS is best-effort.
- **Attachment resolve fails** — attachment is skipped, not the
  message. Better to deliver a partial message than reject the send.
- **Reply parent deleted** — `ReplyParentNotFound` returned; caller
  retries without the reply id.
