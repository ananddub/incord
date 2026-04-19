# Data Model

Storage is split across five backends by access pattern — Postgres for
durable relational entities, Scylla for time-series message data,
Redis for live/ephemeral state, MinIO for blobs, and OpenFGA for
authorization tuples.

## Postgres / TimescaleDB

Migrations live in [db/timescale/migrations/](../db/timescale/migrations).
Apply with `make migrate` or `migrate -path db/timescale/migrations -database $DATABASE_URL up`.

### Identity

**users** — primary identity. Soft-deleted.
| column | type | notes |
|---|---|---|
| id | UUID PK | generated |
| username | VARCHAR(64) UNIQUE | `name#1234` handle |
| email | VARCHAR UNIQUE | |
| password_hash | TEXT | bcrypt |
| display_name | VARCHAR | free-form |
| avatar_url | TEXT | MinIO object key, not a URL |
| bio, background_color, status | TEXT | profile |
| verified | BOOLEAN | email OTP completed |
| deleted | BOOLEAN | soft delete |
| created_at, updated_at | TIMESTAMPTZ | |

Indexes: `idx_users_username`, `idx_users_email`.

**friendships** — directed edges; two rows per accepted friendship.
PK `(user_id, friend_id)`. Status is one of `pending | accepted | blocked`.
Index `idx_friendships_friend` for reverse lookup.

### Guilds

**guilds** — server metadata (id, name, description, icon_url, owner_id,
soft-delete).

**guild_members** — roster. PK `(guild_id, user_id)`. Tracks `nickname`,
`joined_at`, `invite_code`, `invited_by`. Indexed both by guild and by user.

**roles** — per-guild roles with `position` for ordering. Index `idx_roles_guild`.

**role_members** — role assignments. PK `(role_id, user_id)`.

**bans** — PK `(guild_id, user_id)` with reason. Index `idx_bans_user`.

**emojis** — custom guild emojis pointing at MinIO objects.

### Channels

**channels** — all channel kinds, including DMs. Types:
| code | kind |
|---|---|
| 1 | GUILD_TEXT |
| 2 | GUILD_VOICE |
| 3 | GUILD_VIDEO |
| 4 | GUILD_CATEGORY |
| 5 | DM |
| 6 | GROUP_DM |
| 7 | GUILD_ANNOUNCEMENT |
| 8 | GUILD_FORUM |
| 9 | GUILD_STAGE |

DM rows have `guild_id = NULL`. Indexed on `guild_id` and `parent_id`
(for category children).

**channel_permission_overwrites** — Discord-style allow/deny bitmasks
per role or user per channel. `target_type` ∈ `{role, user}`.

**dm_channel_members** — PK `(channel_id, user_id)`; `idx_dm_members_user`
is how we find "all DM channels this user is in".

### Invites

**invites** — PK is the invite code (VARCHAR 10). Carries `max_uses`,
`uses`, `expires_at`. Soft-deleted.

**invite_uses** — append-only join log. Used to render "invited by" and
for guild-admin analytics. Indexed on guild + invite code.

### Media / integrations

**media_files** — upload metadata. `bucket_key` points at MinIO; rows
are created in two phases (unconfirmed → confirmed) so a crashed
client can be cleaned up. Index `idx_media_uploader`.

**webhooks** — placeholder for incoming-webhook integrations.

## ScyllaDB

Schema in [db/scylla/migrations/000001_init.cql](../db/scylla/migrations).
Single keyspace: `ndiscord`.

All message reads are partitioned by `channel_id` so a channel's full
history lives on one set of replicas — the hot path (backfill a
channel) is one partition key lookup.

**messages** — PK `((channel_id), id DESC)` with `id` as `TIMEUUID`. So
"latest N messages in channel" is a single range read, sorted by
default. Columns: `author_id`, `content`, `type`, `reply_to_id`,
`pinned`, `deleted`, `edited_at`, `updated_at`, `created_at`,
`forwarded_from_{channel_id,message_id,author_id}`, `mention_user_ids SET<UUID>`.

**message_attachments** — PK `((channel_id, message_id), id)`. One row
per file attached to a message. Materialised into `ChatAttachment`
entries in the realtime event.

**message_reactions** — PK `((channel_id, message_id), emoji, user_id)`.
Count = row count for `(message_id, emoji)`. Append-only delta; the
"rich snapshot" view is built by the message service before
publishing.

**read_states** — PK `(user_id, channel_id)`. `last_read_message_id`
(TIMEUUID) + `mention_count`. Drives unread badges; updated on
`AckMessage`.

**audit_log** — PK `((guild_id), id DESC)`. Guild moderation trail.

**message_edit_history** — PK `((channel_id, message_id), edited_at DESC)`.
Immutable log of `old_content` for every edit.

## Redis

All keys are set by `infra.Redis` (`github.com/redis/go-redis/v9`). No
key prefix is shared — each subsystem owns its namespace.

| Key | Type | TTL | Writes | Reads |
|---|---|---|---|---|
| `presence:<userID>` | hash `{status, custom_status, last_seen}` | none (cleared on disconnect) | `presence.UpdatePresence`, `OnUserConnect`, `OnUserDisconnect` | `presence.GetPresence`, `GetLiveStatus`, `user.friendPayload` |
| `voice:<channelID>` | hash `{userID → JSON ParticipantState}` | none | `voice.SetParticipant`, `toggleField` | `voice.GetChannelState`, `GetParticipant`, `VoiceSnapshot` |
| `voice:<channelID>:active_since` | int64 unix seconds (SETNX) | none | `voice.EnsureActiveSince` (first joiner) | every VoiceStateEvent includes it so late subscribers get correct session start |
| `typing:<channelID>` | string `userID` | 8s | `message.StartTyping` | clients poll / rely on expiry |
| `refresh:<token>` | string `userID` | 7d (`JWT_REFRESH_TTL`) | `auth.StoreRefreshToken` | `auth.RefreshToken` |
| `otp:<email>` | string 6-digit | 5min | `auth.SendOTP` | `auth.VerifyOTP` (deleted on use) |
| `ratelimit:<identity>:<method>` | int counter | 1min | middleware | middleware |

The `voice:<channelID>` hash is the authoritative store for "is this
user muted / deafened / screensharing" — LiveKit only knows WebRTC-
level state, so mute/video/etc. toggles write here and broadcast from
here.

## MinIO

One bucket named `ndiscord` (`MINIO_BUCKET`). Public-read policy is
on; upload/delete requires a presigned URL. Paths:

| Prefix | Max | Types | Who |
|---|---|---|---|
| `users/<userID>/avatar_<UUID>_<filename>` | 5 MB | jpeg/png/gif/webp | `user.UploadAvatar` |
| `guilds/<guildID>/icon_<UUID>_<filename>` | 5 MB | jpeg/png/gif/webp | `guild.UploadGuildIcon` |
| `uploads/<userID>/<UUID>_<filename>` | unbounded | any | `media.RequestUpload` → presigned PUT |

GETs go through a presigned URL scoped to 7 days. The public endpoint
differs from the internal one — `MINIO_ENDPOINT` is used for writes,
`MINIO_PUBLIC_ENDPOINT` for signing GETs so clients hit the edge
directly.

## OpenFGA (authz)

Tuples are stored in the OpenFGA store named `ndiscord` (auto-created
on startup if `OPENFGA_STORE_ID` is unset). The model defines:

| object type | key relations |
|---|---|
| `user` | — |
| `guild` | `owner`, `admin`, `member` |
| `channel` | `guild` (linking channel → guild), `viewer`, `sender`, `manager` |

Common checks (defined in [internal/shared/authz](../internal/shared/authz)):

- `CanManageGuild(userID, guildID)` — owner or admin
- `CanManageChannels(userID, guildID)` — admin
- `CanViewChannel(userID, channelID)` — member of channel's guild, or DM member
- `CanSendInChannel(userID, channelID)` — view + not muted
- `CanManageChannel(userID, channelID)` — admin

Writes happen on guild create/join/role-assign; reads happen on every
mutating RPC.

## Soft-delete convention

Every Postgres entity that can be "deleted" has a `deleted BOOLEAN`
column; code never issues `DELETE` except in migrations. This keeps
foreign keys honest when a user is removed from a guild while they
still own messages, and makes abuse auditing trivial.

Scylla tables use a `deleted BOOLEAN` column too — no tombstone
churn, just filtered reads.
