# Channel

Guild text/voice/category channels, DM channels, group DMs, channel
permission overwrites. Publishes `CHANNEL_*` events on
`guild.<g>.events` (so they show up in `StreamGuildEvents`) and
lifecycle events for DMs on `dm.<user>.channels`.

## Package

[internal/features/channel](../../internal/features/channel)

## gRPC surface (proto: [channel/v1/channel.proto](../../internal/features/channel/proto/channel/v1/channel.proto))

| RPC | What it does |
|---|---|
| `CreateChannel` | Guild channel (text/voice/category/etc.) — admin-only |
| `GetChannel` / `ListGuildChannels` | Reads |
| `UpdateChannel` | Rename, move, change topic, re-parent |
| `DeleteChannel` | Soft delete |
| `CreateDMChannel` | Open or return an existing 1:1 or group DM |
| `ListDMChannels` | Reads |
| `AddDMGroupMember` / `RemoveDMGroupMember` / `LeaveDMChannel` | Group-DM membership |
| `SetChannelPermissionOverwrite` / `DeleteChannelPermissionOverwrite` / `ListChannelPermissionOverwrites` | Discord-style overrides |

## Data it owns

- `channels` (Postgres — both guild and DM rows)
- `dm_channel_members`
- `channel_permission_overwrites`

## Channel types

Type codes are stable enums used across the wire:

| code | name | guild_id |
|---|---|---|
| 1 | `GUILD_TEXT` | required |
| 2 | `GUILD_VOICE` | required |
| 3 | `GUILD_VIDEO` | required |
| 4 | `GUILD_CATEGORY` | required |
| 5 | `DM` | NULL |
| 6 | `GROUP_DM` | NULL |
| 7 | `GUILD_ANNOUNCEMENT` | required |
| 8 | `GUILD_FORUM` | required |
| 9 | `GUILD_STAGE` | required |

## Guild channel mutations

Every `Create/Update/Delete` publishes a `streamv1.GuildEvent` with:

```go
streamv1.GuildEvent{
    Event:     streamv1.GuildEventType_GUILD_EVENT_CHANNEL_CREATE, // or UPDATE/DELETE
    GuildId:   guildID,
    ChannelId: ch.ID.String(),
    Name:      ch.Name,
    Type:      int64(ch.Type),
    Topic:     ch.Topic,
    Position:  ch.Position,
    ParentId:  uuidToString(ch.ParentID),
}
```

Subject: `guild.<guildID>.events` — consumed by every member's
`StreamGuildEvents`.

Authz: `CanManageChannels(caller, guildID)` is required for
create/update/delete. On create, an additional tuple is written:
`authz.SetChannelGuild(channelID, guildID)` so channel-scoped checks
can resolve to guild checks.

## DM flow

### 1:1 DM auto-open

`CreateDMChannel(userID, [recipientID])`:

1. Try `GetDMChannelBetweenUsers(user, recipient)` — short-circuit
   return if the pair already has a channel (idempotent).
2. Otherwise create a channel row (`type = 5`), add both members.
3. `publishDMChannelCreated` emits `CHANNEL_LIFECYCLE_CREATE` on
   `dm.<userID>.channels` and `dm.<recipientID>.channels` so both
   clients' `StreamDmChannels` subscribers see it immediately.

### Group DM

Same as above but with multiple recipients and `type = 6`. Members
can be added later via `AddDMGroupMember`, which fires
`CHANNEL_LIFECYCLE_UPDATE` on every current member's subject so the
participant list refreshes.

### Leave / remove

`LeaveDMChannel`, `RemoveDMGroupMember`, or the owner deleting the
channel — all publish `CHANNEL_LIFECYCLE_DELETE` (for leaves the
event goes to the leaving user only; for group-DM kicks everyone
gets an `UPDATE` with the updated member list).

## `publishDMChannelEvent` details

```go
payload := &streamv1.DmChannelEvent{
    Type:        eventType,
    Id:          uuidToString(ch.ID),
    Name:        ch.Name,
    ChannelType: int32(ch.Type),
    Members:     members,
}
```

For lifecycle `DELETE` the `members` list is nil — clients already
know who was in the channel and use the event to drop it from their
sidebar.

## Permission overwrites

`channel_permission_overwrites` stores Discord-style allow/deny rules
per `(channel, target, permission)` where target is a role or a user.
The authz package applies these on top of the guild-level grants with
Discord's precedence: **user deny > user allow > role deny > role
allow > guild result**. Owner and `ADMINISTRATOR` holders bypass
overrides entirely.

**RPCs** (require `MANAGE_CHANNELS` on the parent guild):

- `SetChannelOverride(channel_id, target_type, target_id, permission, effect)`
- `DeleteChannelOverride(channel_id, target_type, target_id, permission)`
- `ListChannelOverrides(channel_id)`

Every mutation broadcasts `GUILD_EVENT_CHANNEL_UPDATE` on
`guild.<G>.events` so subscribed members refetch the channel's
effective permissions. For private channels, this is also where the
stream-side fan-out filter picks up access changes in real time.

## Cross-feature wiring

- `channel.NewDMResolver(channelSvc)` implements `DMOpener` (used by
  `user.AcceptFriendRequest` to auto-open the DM) and
  `DMChannelResolver` (used by `message.SendDirectMessage`).
- `channel.NewDMChannelMembersResolver(channelRepo)` implements
  `DMChannelLister` for `message` fan-out.
- `channel.NewGuildResolver(channelSvc)` resolves a channel → guild
  for the message handler.

## Failure modes

- **Create channel → authz write fails** → channel row exists with
  no guild tuple. Same consistency gap as guild creation; a periodic
  repair job should add missing tuples (TODO).
- **Deleting a channel with an active voice room** — voice cleanup
  is lazy: the next LiveKit `room_finished` webhook tears down
  Redis; clients get `CHANNEL_DELETE` first and disconnect on their
  own.
