# nDiscord API Usage Guide

## Authentication

**Public endpoints** (no auth required):
- `auth.v1.AuthService/Register`
- `auth.v1.AuthService/VerifyOTP`
- `auth.v1.AuthService/ResendOTP`
- `auth.v1.AuthService/Login`
- `auth.v1.AuthService/RefreshToken`
- `auth.v1.AuthService/ValidateToken`
- `guild.v1.GuildService/PreviewInvite`

All other endpoints require: `Authorization: Bearer <token>`

---

## Auth Service

| RPC | Description | Request | Response |
|-----|-------------|---------|----------|
| **Register** | Send OTP to email | `username`, `email`, `password` | `user_id`, `message` |
| **VerifyOTP** | Activate account + get tokens | `email`, `otp` | `user_id`, `access_token`, `refresh_token` |
| **ResendOTP** | Resend OTP | `email` | `message` |
| **Login** | Authenticate | `email`, `password` | `user_id`, `access_token`, `refresh_token` |
| **RefreshToken** | Rotate tokens | `refresh_token` | `access_token`, `refresh_token` |
| **Logout** | Invalidate refresh | `refresh_token` | (empty) |
| **ValidateToken** | Check token validity | `access_token` | `user_id`, `valid` |

---

## User Service

| RPC | Description | Request | Response |
|-----|-------------|---------|----------|
| **GetUser** | Fetch profile by ID | `user_id` (UUID) | `User` |
| **UpdateUser** | Update profile | `avatar_url?`, `bio?`, `status?`, `background_color?`, `display_name?` | `User` |
| **UpdateUsername** | Set handle once (name#1234) | `username` (2-32) | `User` |
| **UpdateStatus** | Set custom status | `status` (<=128) | `User` |
| **UploadUserAvatar** | Upload avatar | `filename`, `content_type`, `data` (<=5MB) | `avatar_url`, `User` |
| **DeleteUser** | Deactivate account | `user_id` | (empty) |
| **GetUserByUsername** | Lookup by handle | `username` (name#1234) | `User` |
| **SearchUsers** | Search users | `query`, `limit`, `offset` | `users[]`, `total` |
| **SendFriendRequest** | Send friend request | `target_username` (name#1234) | `Friendship` |
| **AcceptFriendRequest** | Accept request | `requester_user_id` | `Friendship` |
| **DeclineFriendRequest** | Decline request | `requester_user_id` | (empty) |
| **CancelFriendRequest** | Withdraw outgoing | `target_user_id` | (empty) |
| **RemoveFriend** | Unfriend | `friend_id` | (empty) |
| **BlockUser** | Block user | `target_user_id` | (empty) |
| **UnblockUser** | Unblock user | `target_user_id` | (empty) |
| **ListFriends** | Get friends | (empty) | `friends[]` |
| **ListPendingRequests** | Get pending | (empty) | `incoming[]`, `outgoing[]` |
| **ListBlocked** | Get blocked | (empty) | `blocked[]` |

**User fields:** `id`, `username` (name#1234), `display_name`, `email`, `avatar_url`, `bio`, `status`, `background_color`, `created_at`, `updated_at`

---

## Guild Service

| RPC | Description | Request | Response |
|-----|-------------|---------|----------|
| **CreateGuild** | Create server | `name`, `description`, `icon_url` | `Guild` |
| **GetGuild** | Fetch guild | `guild_id` | `Guild` |
| **UpdateGuild** | Update guild | `guild_id`, `name?`, `description?`, `icon_url?`, `banner_url?` | `Guild` |
| **UploadGuildIcon** | Upload icon (profile image, ≤5MB) | `guild_id`, `filename`, `content_type`, `data` | `icon_url`, `Guild` |
| **UploadGuildBanner** | Upload banner (hero / background image, ≤10MB) | `guild_id`, `filename`, `content_type`, `data` | `banner_url`, `Guild` |
| **DeleteGuild** | Delete (owner only) | `guild_id` | (empty) |
| **ListUserGuilds** | Get user's guilds | `user_id` | `guilds[]` |
| **PreviewInvite** | View invite (NO AUTH) | `invite_code` | `InvitePreview` |
| **JoinGuild** | Join via invite (idempotent) | `invite_code` | `Guild`, `channels[]`, `member_count` |
| **LeaveGuild** | Leave server | `guild_id` | (empty) |
| **ListMembers** | Get members | `guild_id`, `limit`, `after` | `GuildMember[]` |
| **KickMember** | Kick member | `guild_id`, `user_id` | (empty) |
| **BanMember** | Ban member | `guild_id`, `user_id`, `reason` | (empty) |
| **UnbanMember** | Unban | `guild_id`, `user_id` | (empty) |
| **CreateInvite** | Generate invite | `guild_id`, `channel_id`, `max_uses`, `max_age_seconds` | `Invite` |
| **CreateRole** | Create role | `guild_id`, `name`, `color` | `Role` |
| **UpdateRole** | Modify role | `role_id`, `guild_id`, `name?`, `color?`, `position?` | `Role` |
| **DeleteRole** | Remove role | `guild_id`, `role_id` | (empty) |
| **AssignRole** | Give role | `guild_id`, `user_id`, `role_id` | (empty) |
| **RemoveRole** | Revoke role | `guild_id`, `user_id`, `role_id` | (empty) |
| **TransferOwnership** | Change owner | `guild_id`, `new_owner_id` | `Guild` |
| **GrantPermission** | Grant permission | `guild_id`, `user_id`, `permission` | (empty) |
| **RevokePermission** | Revoke permission | `guild_id`, `user_id`, `permission` | (empty) |
| **GetUserPermissions** | List permissions | `guild_id`, `user_id` | `permissions[]` |

---

## Channel Service

| RPC | Description | Request | Response |
|-----|-------------|---------|----------|
| **CreateChannel** | Create guild channel | `guild_id`, `name`, `type`, `topic?`, `parent_id?` | `Channel` |
| **GetChannel** | Fetch channel | `channel_id` | `Channel` |
| **UpdateChannel** | Modify channel | `channel_id`, `name?`, `topic?`, `position?`, `parent_id?` | `Channel` |
| **DeleteChannel** | Remove channel | `channel_id` | (empty) |
| **ListGuildChannels** | Get all channels | `guild_id` | `channels[]` |
| **CreateDMChannel** | Start DM | `recipient_ids[]` (1-10) | `Channel` |
| **ListDMChannels** | Get user's DMs | (empty) | `channels[]` |
| **ListDMChannelMembers** | Get DM members | `channel_id` | `DMMember[]` |
| **ListDMChannelsWithMembers** | DMs + members in one call | (empty) | `DMChannelWithMembers[]` |
| **AddDMGroupMember** | Add to group DM | `channel_id`, `user_id` | (empty) |
| **RemoveDMGroupMember** | Remove from group DM | `channel_id`, `user_id` | (empty) |

**Channel types:** TEXT(1), VOICE(2), VIDEO(3), CATEGORY(4), DM(5), GROUP_DM(6), ANNOUNCEMENT(7), FORUM(8), STAGE(9)

---

## Message Service

| RPC | Description | Request | Response |
|-----|-------------|---------|----------|
| **SendMessage** | Post message | `channel_id`, `content`, `type?`, `reply_to_id?`, `attachment_ids[]?`, `forwarded_from?`, `mention_user_ids[]?` | `Message` |
| **GetMessage** | Fetch message | `channel_id`, `message_id` | `Message` |
| **EditMessage** | Edit (24h window) | `channel_id`, `message_id`, `content` | `Message` |
| **DeleteMessage** | Delete + cascade | `channel_id`, `message_id` | (empty) |
| **ListMessages** | Paginated messages | `channel_id`, `before?`, `after?`, `limit` | `messages[]` |
| **PinMessage** | Pin message | `channel_id`, `message_id` | (empty) |
| **UnpinMessage** | Unpin | `channel_id`, `message_id` | (empty) |
| **AddReaction** | React with emoji | `channel_id`, `message_id`, `emoji` | (empty) |
| **RemoveReaction** | Remove reaction | `channel_id`, `message_id`, `emoji` | (empty) |
| **AckMessage** | Mark as read | `channel_id`, `message_id` | (empty) |
| **StartTyping** | Typing indicator | `channel_id` | (empty) |
| **SendDirectMessage** | DM (auto-creates channel) | `recipient_id`, `content` | `channel_id`, `Message` |
| **GetUnreadCounts** | All unread counts | (empty) | `dm_messages[]`, `channel_messages[]`, `total_unread` |
| **SearchMessages** | Search in channel | `channel_id`, `query`, `limit` | `messages[]`, `total` |
| **GetThreadMessages** | Get replies | `channel_id`, `parent_message_id`, `limit` | `messages[]` |
| **BulkDeleteMessages** | Delete multiple | `channel_id`, `message_ids[]` | `deleted_count` |
| **GetEditHistory** | View edit history | `channel_id`, `message_id` | `MessageEdit[]` |

**Message fields:** `id`, `channel_id`, `author_id`, `content`, `type`, `attachments[]`, `reactions[]`, `reply_to_id`, `pinned`, `created_at`, `edited_at`, `forwarded_from?`, `mention_user_ids[]`

---

## Media Service

| RPC | Description | Request | Response |
|-----|-------------|---------|----------|
| **RequestUpload** | Get presigned PUT URL | `filename`, `content_type`, `size` (<=25MB) | `upload_id`, `upload_url` |
| **ConfirmUpload** | Finalize upload | `upload_id` | `file_id`, `url` |
| **GetDownloadURL** | Get download URL (permanent) | `file_id` | `url`, `expires_at` |
| **DeleteFile** | Remove file | `file_id` | (empty) |

---

## Voice Service

| RPC | Description | Request | Response |
|-----|-------------|---------|----------|
| **JoinChannel** | Get LiveKit token | `guild_id`, `channel_id` | `url`, `token`, `room`, `expires_in` |
| **LeaveChannel** | Leave voice | `guild_id`, `channel_id` | (empty) |
| **GetChannelParticipants** | Active participants | `channel_id` | `VoiceParticipant[]` |
| **StartDMCall** | Initiate DM call | `channel_id`, `video?` | `url`, `token`, `room`, `expires_in` |
| **JoinDMCall** | Accept DM call | `channel_id`, `video?` | `url`, `token`, `room`, `expires_in` |
| **RejectDMCall** | Decline call | `channel_id` | (empty) |
| **LeaveDMCall** | Hang up | `channel_id` | (empty) |
| **ServerMuteUser** | Admin mute | `channel_id`, `target_user_id`, `muted` | (empty) |
| **ServerDeafenUser** | Admin deafen | `channel_id`, `target_user_id`, `deafened` | (empty) |
| **DisconnectUser** | Admin kick from voice | `channel_id`, `target_user_id` | (empty) |

**Voice state** managed via LiveKit webhook → NATS → StreamVoiceState. Client uses LiveKit SDK for mute/video/screen — server observes + relays.

---

## Stream Service (Real-time)

| RPC | Events | NATS subject |
|-----|--------|--------------|
| **StreamDmChat** | create/update/delete/pin/unpin/reaction_add/reaction_remove/read_receipt | `dm.{userId}.message.*` |
| **StreamDmChannels** | create/update/delete (DM lifecycle) | `dm.{userId}.channels` |
| **StreamDmCalls** | call_incoming/accepted/rejected/ended | `dm.{userId}.call` |
| **StreamTextChannels** | Guild text messages (same as DmChat + guild_id) | `guild.{guildId}.channel.message.*` |
| **StreamVoiceChat** | Text in voice channels | `guild.{guildId}.channel.voicechat.*` |
| **StreamGuildEvents** | member_add/remove/ban, role changes, channel create/update/delete, guild update/delete | `guild.{guildId}.events` |
| **StreamVoiceState** | join/leave/track_update/state_sync (mute/video/screen) | `guild.{guildId}.channel.voice.*` |
| **StreamTyping** | Typing indicators | `guild.{guildId}.channel.typing.*` + `dm.{userId}.typing.*` |
| **StreamFriendActivity** | presence_update/profile_update/friend_request/accepted/declined/removed | `friend.{userId}.activity` |

---

## Example Flows

### 1. Register + Login
```
Register(username="alice", email="alice@ex.com", password="secret123")
→ {user_id, message: "OTP sent"}

VerifyOTP(email="alice@ex.com", otp="123456")
→ {user_id, access_token, refresh_token}

UpdateUsername(username="alice")
→ {user: {username: "alice#4837", display_name: "alice"}}
```

### 2. Guild + Categories + Channels
```
CreateGuild(name="My Server") → {guild}

CreateChannel(guild_id, name="TEXT CHANNELS", type=CATEGORY) → {cat_id}
CreateChannel(guild_id, name="general", type=TEXT, parent_id=cat_id)
CreateChannel(guild_id, name="random", type=TEXT, parent_id=cat_id)

CreateChannel(guild_id, name="VOICE", type=CATEGORY) → {vc_cat_id}
CreateChannel(guild_id, name="General", type=VOICE, parent_id=vc_cat_id)

ListGuildChannels(guild_id) → client groups by parent_id
```

### 3. Send DM with Attachment
```
RequestUpload(filename="pic.png", content_type="image/png", size=102400)
→ {upload_id, upload_url}

HTTP PUT <upload_url> with file bytes

ConfirmUpload(upload_id) → {file_id, url}

SendMessage(channel_id=dm_id, content="Look!", attachment_ids=[file_id])
→ {message with attachments[]}
```

### 4. Voice Channel
```
JoinChannel(guild_id, channel_id) → {url, token, room}
[Client connects LiveKit SDK with token]
[Mute/video/screen via LiveKit SDK — webhook syncs state to NATS]
LeaveChannel(guild_id, channel_id)
```

### 5. Invite Flow
```
CreateInvite(guild_id, channel_id, max_uses=10) → {invite.url}

[Share URL: https://ndiscord.app/invite/kX9mPq]

PreviewInvite(invite_code="kX9mPq") → {guild_name, member_count, ...}  // NO AUTH

JoinGuild(invite_code="kX9mPq") → {guild, channels[], member_count}
```
