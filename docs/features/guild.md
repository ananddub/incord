# Guild

Guilds (servers), their membership, roles, bans, icons and invite
codes. Every mutation emits a `streamv1.GuildEvent` on
`guild.<guildID>.events`.

## Package

[internal/features/guild](../../internal/features/guild)

## gRPC surface (proto: [guild/v1/guild.proto](../../internal/features/guild/proto/guild/v1/guild.proto))

| RPC | What it does |
|---|---|
| `CreateGuild` | New guild, add owner as member + role, seed authz tuples |
| `GetGuild` / `ListUserGuilds` / `SearchGuilds` | Reads |
| `UpdateGuild` | Rename / description (admin) |
| `DeleteGuild` | Owner-only soft delete |
| `UploadGuildIcon` | Upload image → MinIO, persist key |
| `AddMember` / `KickMember` | Manual membership (via invite is below) |
| `CreateInvite` / `ListInvites` / `RevokeInvite` | Invite code lifecycle |
| `PreviewInvite` | Public — renders invite landing page |
| `JoinByInvite` | Consume invite, become guild member |
| `BanMember` / `UnbanMember` / `ListBans` | Ban list |
| `CreateRole` / `UpdateRole` / `DeleteRole` / `AssignRole` / `RemoveRole` / `ListRoles` | Role CRUD and assignment |

## Data it owns

- `guilds`, `guild_members`, `roles`, `role_members`, `bans`, `invites`, `invite_uses` (Postgres)
- MinIO `guilds/<id>/icon_…`
- OpenFGA tuples under `guild:<id>` and `channel:<id>`

## Central helper — `publishGuildEvent`

```go
func (s *Service) publishGuildEvent(
    ctx context.Context,
    guildID pgtype.UUID,
    action streamv1.GuildEventType,
    extra map[string]string,
)
```

Builds a typed `*streamv1.GuildEvent` with `event = action`,
`action = action`, copies well-known keys from `extra`
(`user_id`, `channel_id`, `name`, `icon_url`, `role_id`, `reason`,
`parent_id`, `topic`) into strongly-typed fields, and publishes to
`guild.<id>.events`.

All mutations use the enum — raw strings are banned. Typos fail at
compile time.

## Event catalogue emitted

| Mutation | GuildEventType |
|---|---|
| `AddMember` / accept invite | `GUILD_EVENT_MEMBER_ADD` |
| `KickMember` / `LeaveGuild` | `GUILD_EVENT_MEMBER_REMOVE` |
| `BanMember` | `GUILD_EVENT_MEMBER_BAN` |
| `UnbanMember` | `GUILD_EVENT_MEMBER_UNBAN` |
| `UpdateGuild` / `UploadGuildIcon` | `GUILD_EVENT_UPDATE` |
| `DeleteGuild` | `GUILD_EVENT_DELETE` |
| `CreateRole` | `GUILD_EVENT_ROLE_CREATE` |
| `UpdateRole` | `GUILD_EVENT_ROLE_UPDATE` |
| `DeleteRole` | `GUILD_EVENT_ROLE_DELETE` |
| `AssignRole` | `GUILD_EVENT_ROLE_ASSIGN` |
| `RemoveRole` | `GUILD_EVENT_ROLE_REMOVE` |

Channel events (`CHANNEL_CREATE/UPDATE/DELETE`) come from the
**channel** service but land on the same guild subject — see
[channel.md](./channel.md).

## Invite flow

```
owner/admin.CreateInvite(guildID, channelID, maxUses, expiresAt)
     │
     └─ random 10-char alphanumeric code → INSERT invites
     │   Returns BuildInviteURL = "<INVITE_BASE_URL>/<code>"
     ↓
newuser.PreviewInvite(code)                    (public — no auth)
     └─ reads guild + inviter profile + guessed member count
     │   Clients render the landing page with this
     ↓
newuser.JoinByInvite(code)                     (auth required)
     │
     ├─ lock invite row FOR UPDATE
     ├─ check expires_at, uses < max_uses
     ├─ INSERT guild_members
     ├─ INSERT invite_uses (audit)
     ├─ UPDATE invites SET uses = uses + 1
     ├─ authz.AddGuildMember tuple
     ├─ publishGuildEvent MEMBER_ADD
     └─ idempotent: already-member re-join returns guild data instead
```

## Roles and permissions

Roles are plain Postgres rows. The **authz** side of things is handled
by OpenFGA:

- `CreateRole` writes a row but does NOT add any authz tuple by itself
  — role membership maps to permissions via the OpenFGA model
  separately.
- `AssignRole` writes a `role_members` row and an authz tuple if the
  role implies a high-level capability (e.g. "admin").

Authz checks happen in other services via `authz.Can…` calls.

## Icon upload

Identical shape to avatar upload in the user feature:

1. `UploadGuildIcon(callerID, guildID, filename, contentType, data)`.
2. Size ≤ 5 MB, content type ∈ {jpeg, png, gif, webp}.
3. `authz.CanManageGuild(caller, guild)` check.
4. MinIO PUT to `guilds/<guildID>/icon_<UUID>_<filename>`.
5. `UPDATE guilds SET icon_url = <bucket_key>`.
6. `publishGuildEvent(GUILD_EVENT_UPDATE, {icon_url: presignedURL})`.

`ResolveIconURL(key)` returns the 7-day presigned URL; clients
don't see the bucket key.

## Authz wiring

On `CreateGuild`:

```go
authz.AddGuildOwner(ctx, ownerID, guildID)
authz.AddGuildMember(ctx, ownerID, guildID)
```

On `AddMember` / `JoinByInvite`:

```go
authz.AddGuildMember(ctx, userID, guildID)
```

On `KickMember`:

```go
authz.RemoveGuildMember(ctx, userID, guildID)
```

Every permission check (`CanManageGuild`, `CanManageChannels`, …) is a
call into OpenFGA — the service never reads `guild_members` to decide.

## Failure modes

- **Invite race** — two users claiming the last seat. Handled by
  `SELECT … FOR UPDATE` on the invite row inside a transaction.
- **Authz write fails after guild row commits** — logged; the guild
  exists with no owner tuple. Next startup's consistency check
  (TODO) should repair. Operationally rare — OpenFGA sharing the
  same availability as Postgres.
- **Delete-while-join** — `DeleteGuild` sets `deleted=true`; an
  in-flight `JoinByInvite` may succeed but the guild is tombstoned.
  Clients that opened the stream before delete get `GUILD_DELETE`
  and clean up; new joiners should see 404.
