// Package authz is a thin Postgres-backed RBAC layer used by every
// feature service to gate mutating RPCs.
//
// The authorisation data lives in three tables:
//
//   - roles             — per-guild, already exists
//   - permissions       — catalogue of Discord-style permission names
//   - role_permissions  — join: which permissions each role grants
//
// Guild owner (guilds.owner_id) always passes every check. Admin is just
// a permission named ADMINISTRATOR that short-circuits any specific
// permission lookup when a user has it through any assigned role.
//
// The exported API is intentionally identical to the previous OpenFGA
// client so no feature service had to change when we swapped the backend:
// typed Can* wrappers, a generic Check, and tuple-style Write/Delete
// helpers (now no-ops since the data is derived from the Postgres rows
// guild/role/member code already writes).
package authz

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// Client resolves permission checks against Postgres. Safe to share
// across goroutines; the underlying pool + caches are concurrent.
type Client struct {
	pool *pgxpool.Pool

	// channel → guild cache on the hot path. Channels rarely re-parent,
	// so a small in-process LRU removes one query per message delivery
	// without risking stale decisions for longer than guildCacheTTL.
	chanGuildMu    sync.RWMutex
	chanGuildCache map[string]chanGuildEntry
}

type chanGuildEntry struct {
	guildID   string // empty string == DM / guildless
	expiresAt time.Time
}

const (
	chanGuildCacheTTL = 5 * time.Minute
	chanGuildCacheMax = 5000
)

// NewClient returns a Client backed by the shared Postgres pool. Nil is
// a permitted argument for tests / degraded mode — every method on a nil
// Client allows the action (fail-open) so services never block on a
// missing authz layer during tests.
func NewClient(pool *pgxpool.Pool) *Client {
	return &Client{
		pool:           pool,
		chanGuildCache: make(map[string]chanGuildEntry),
	}
}

// ── Key helpers (kept for call-site compatibility with the previous
// OpenFGA client; some callers still stringify via these). ────────────

func UserKey(userID string) string       { return "user:" + userID }
func GuildKey(guildID string) string     { return "guild:" + guildID }
func ChannelKey(channelID string) string { return "channel:" + channelID }
func RoleKey(roleID string) string       { return "role:" + roleID }

// RoleMembersKey is retained for API parity but no longer meaningful —
// a role's members are a direct row in role_members now.
func RoleMembersKey(roleID string) string { return "role:" + roleID + "#member" }

// ── Permission-name mapping ───────────────────────────────────────────

// PermissionRelation maps Discord permission enum names (KICK_MEMBERS,
// VIEW_CHANNELS, …) to the can_* relation strings used throughout the
// service code. Kept public so callers can translate in either direction.
var PermissionRelation = map[string]string{
	"VIEW_CHANNELS":                       "can_view_channels",
	"SEND_MESSAGES":                       "can_send_messages",
	"MANAGE_MESSAGES":                     "can_manage_messages",
	"MANAGE_CHANNELS":                     "can_manage_channels",
	"MANAGE_GUILD":                        "can_manage_guild",
	"KICK_MEMBERS":                        "can_kick",
	"BAN_MEMBERS":                         "can_ban",
	"MANAGE_ROLES":                        "can_manage_roles",
	"MANAGE_INVITES":                      "can_manage_invites",
	"ADD_REACTIONS":                       "can_add_reactions",
	"CONNECT":                             "can_connect",
	"SPEAK":                               "can_speak",
	"STREAM":                              "can_stream",
	"MUTE_MEMBERS":                        "can_mute_members",
	"DEAFEN_MEMBERS":                      "can_deafen_members",
	"MENTION_EVERYONE":                    "can_mention_everyone",
	"MANAGE_EMOJIS":                       "can_manage_emojis",
	"MANAGE_WEBHOOKS":                     "can_manage_webhooks",
	"VIEW_AUDIT_LOG":                      "can_view_audit_log",
	"VIEW_GUILD_INSIGHTS":                 "can_view_guild_insights",
	"MANAGE_NICKNAMES":                    "can_manage_nicknames",
	"CHANGE_NICKNAME":                     "can_change_nickname",
	"CREATE_GUILD_EXPRESSIONS":            "can_create_guild_expressions",
	"MODERATE_MEMBERS":                    "can_moderate_members",
	"VIEW_CREATOR_MONETIZATION_ANALYTICS": "can_view_monetization",
	"SEND_TTS_MESSAGES":                   "can_send_tts",
	"EMBED_LINKS":                         "can_embed_links",
	"ATTACH_FILES":                        "can_attach_files",
	"READ_MESSAGE_HISTORY":                "can_read_message_history",
	"USE_EXTERNAL_EMOJIS":                 "can_use_external_emojis",
	"USE_EXTERNAL_STICKERS":               "can_use_external_stickers",
	"USE_EXTERNAL_SOUNDS":                 "can_use_external_sounds",
	"USE_EXTERNAL_APPS":                   "can_use_external_apps",
	"PRIORITY_SPEAKER":                    "can_priority_speaker",
	"MOVE_MEMBERS":                        "can_move_members",
	"USE_VAD":                             "can_use_vad",
	"REQUEST_TO_SPEAK":                    "can_request_to_speak",
	"USE_SOUNDBOARD":                      "can_use_soundboard",
	"USE_EMBEDDED_ACTIVITIES":             "can_use_embedded_activities",
	"SEND_VOICE_MESSAGES":                 "can_send_voice_messages",
	"SEND_POLLS":                          "can_send_polls",
	"PIN_MESSAGES":                        "can_pin_messages",
	"BYPASS_SLOWMODE":                     "can_bypass_slowmode",
	"CREATE_PUBLIC_THREADS":               "can_create_public_threads",
	"CREATE_PRIVATE_THREADS":              "can_create_private_threads",
	"SEND_MESSAGES_IN_THREADS":            "can_send_messages_in_threads",
	"MANAGE_THREADS":                      "can_manage_threads",
	"CREATE_EVENTS":                       "can_create_events",
	"MANAGE_EVENTS":                       "can_manage_events",
	"ADMINISTRATOR":                       "admin",
}

// relationToName is the reverse lookup: "can_kick" → "KICK_MEMBERS".
// Built at init time; lookups are O(1).
var relationToName = func() map[string]string {
	m := make(map[string]string, len(PermissionRelation))
	for name, rel := range PermissionRelation {
		m[rel] = name
	}
	return m
}()

// ── Core check ────────────────────────────────────────────────────────

// checkSQL resolves every Can* question in a single round trip:
//  1. Is $1 the guild's owner? → allow
//  2. Does any role $1 is assigned to grant either the specific
//     permission $3 or the blanket ADMINISTRATOR? → allow
//  3. Otherwise → deny
//
// The existential is cheap because all joins hit PKs / covering indexes.
const checkSQL = `
SELECT EXISTS (
    SELECT 1
      FROM guilds g
     WHERE g.id = $2
       AND g.owner_id = $1
       AND g.deleted = FALSE
    UNION ALL
    SELECT 1
      FROM role_members rm
      JOIN roles r            ON r.id = rm.role_id
                              AND r.guild_id = $2
                              AND r.deleted = FALSE
      JOIN role_permissions rp ON rp.role_id = r.id
      JOIN permissions p       ON p.id = rp.permission_id
     WHERE rm.user_id = $1
       AND rm.deleted = FALSE
       AND p.name IN ($3, 'ADMINISTRATOR')
)`

// Check reports whether userKey has the given `can_*` relation on
// objectKey. Kept key-prefixed for backwards compat with call sites
// that still string-format via UserKey/GuildKey helpers. Unknown
// relations deny.
//
// nil Client fails open so tests and degraded-mode paths aren't blocked.
func (c *Client) Check(ctx context.Context, userKey, relation, objectKey string) bool {
	if c == nil {
		return true
	}
	userID := strings.TrimPrefix(userKey, "user:")
	guildID := strings.TrimPrefix(objectKey, "guild:")
	permName, ok := relationToName[relation]
	if !ok {
		logger.Log.Warn().Str("relation", relation).Msg("authz: unknown relation, denying")
		return false
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return false
	}
	gid, err := parseUUID(guildID)
	if err != nil {
		return false
	}
	var allowed bool
	if err := c.pool.QueryRow(ctx, checkSQL, uid, gid, permName).Scan(&allowed); err != nil {
		logger.Log.Error().Err(err).Str("user", userID).Str("guild", guildID).Str("perm", permName).Msg("authz check query failed")
		return false
	}
	return allowed
}

// ── Typed guild-scoped wrappers. Same signatures, same relation
// strings, same behaviour as the previous OpenFGA client so no feature
// service had to change. ──────────────────────────────────────────────

func (c *Client) CanManageGuild(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_manage_guild", GuildKey(guildID))
}
func (c *Client) CanKick(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_kick", GuildKey(guildID))
}
func (c *Client) CanBan(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_ban", GuildKey(guildID))
}
func (c *Client) CanManageRoles(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_manage_roles", GuildKey(guildID))
}
func (c *Client) CanManageChannels(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_manage_channels", GuildKey(guildID))
}
func (c *Client) CanManageInvites(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_manage_invites", GuildKey(guildID))
}
func (c *Client) CanAddReactions(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_add_reactions", GuildKey(guildID))
}
func (c *Client) CanPinMessages(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_pin_messages", GuildKey(guildID))
}
func (c *Client) CanAttachFiles(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_attach_files", GuildKey(guildID))
}
func (c *Client) CanMentionEveryone(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_mention_everyone", GuildKey(guildID))
}
func (c *Client) CanReadMessageHistory(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_read_message_history", GuildKey(guildID))
}
func (c *Client) CanConnect(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_connect", GuildKey(guildID))
}
func (c *Client) CanSpeak(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_speak", GuildKey(guildID))
}
func (c *Client) CanStream(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_stream", GuildKey(guildID))
}
func (c *Client) CanMuteMembers(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_mute_members", GuildKey(guildID))
}
func (c *Client) CanDeafenMembers(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_deafen_members", GuildKey(guildID))
}
func (c *Client) CanMoveMembers(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_move_members", GuildKey(guildID))
}

// CanGuildPermission is the generic helper for permissions without a
// dedicated typed wrapper — accepts a `can_*` relation string directly.
func (c *Client) CanGuildPermission(ctx context.Context, userID, guildID, relation string) bool {
	return c.Check(ctx, UserKey(userID), relation, GuildKey(guildID))
}

// ── Channel-scoped wrappers. Resolve the channel's guild (with LRU
// cache) then delegate to the guild-scoped check. DM channels have
// no guild; they fall back to DM membership. ──────────────────────────

func (c *Client) CanViewChannel(ctx context.Context, userID, channelID string) bool {
	return c.checkChannel(ctx, userID, channelID, "can_view_channels")
}
func (c *Client) CanSendInChannel(ctx context.Context, userID, channelID string) bool {
	return c.checkChannel(ctx, userID, channelID, "can_send_messages")
}
func (c *Client) CanManageChannel(ctx context.Context, userID, channelID string) bool {
	return c.checkChannel(ctx, userID, channelID, "can_manage_channels")
}

func (c *Client) checkChannel(ctx context.Context, userID, channelID, relation string) bool {
	if c == nil {
		return true
	}
	guildID, isDM, err := c.resolveChannelGuild(ctx, channelID)
	if err != nil {
		logger.Log.Debug().Err(err).Str("channel", channelID).Msg("authz: channel lookup failed")
		return false
	}
	if isDM {
		return c.isDMMember(ctx, userID, channelID)
	}
	return c.Check(ctx, UserKey(userID), relation, GuildKey(guildID))
}

// resolveChannelGuild returns the channel's parent guild_id (or isDM=true
// for guildless channels). Backed by a small in-process LRU since channels
// rarely change parent and this sits on every message / typing / voice
// delivery.
func (c *Client) resolveChannelGuild(ctx context.Context, channelID string) (string, bool, error) {
	c.chanGuildMu.RLock()
	if e, ok := c.chanGuildCache[channelID]; ok && time.Now().Before(e.expiresAt) {
		c.chanGuildMu.RUnlock()
		return e.guildID, e.guildID == "", nil
	}
	c.chanGuildMu.RUnlock()

	chUUID, err := parseUUID(channelID)
	if err != nil {
		return "", false, err
	}
	var guildID pgtype.UUID
	err = c.pool.QueryRow(ctx, `SELECT guild_id FROM channels WHERE id = $1 AND deleted = FALSE`, chUUID).Scan(&guildID)
	if err != nil {
		return "", false, err
	}
	result := ""
	if guildID.Valid {
		result = uuidToStr(guildID)
	}
	c.cacheChannelGuild(channelID, result)
	return result, result == "", nil
}

func (c *Client) cacheChannelGuild(channelID, guildID string) {
	c.chanGuildMu.Lock()
	defer c.chanGuildMu.Unlock()
	// Coarse eviction: if over capacity, blow away the map and rebuild
	// lazily. Simpler than a full LRU, and the cache is warm again in
	// one active-user's worth of traffic.
	if len(c.chanGuildCache) >= chanGuildCacheMax {
		c.chanGuildCache = make(map[string]chanGuildEntry, chanGuildCacheMax)
	}
	c.chanGuildCache[channelID] = chanGuildEntry{
		guildID:   guildID,
		expiresAt: time.Now().Add(chanGuildCacheTTL),
	}
}

// isDMMember checks dm_channel_members for the (channel, user) pair.
// Used as the DM fallback for channel-scoped permission checks since
// DM channels don't sit inside a guild permission graph.
func (c *Client) isDMMember(ctx context.Context, userID, channelID string) bool {
	uid, err := parseUUID(userID)
	if err != nil {
		return false
	}
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return false
	}
	var exists bool
	err = c.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM dm_channel_members
		                WHERE channel_id = $1 AND user_id = $2 AND deleted = FALSE)`,
		chUUID, uid).Scan(&exists)
	if err != nil {
		return false
	}
	return exists
}

// ── Role mutation helpers ─────────────────────────────────────────────

// AddUserToRole assigns a user to a role (idempotent: re-activates a
// soft-deleted row). The source of truth for role assignments.
func (c *Client) AddUserToRole(ctx context.Context, userID, roleID string) error {
	if c == nil {
		return nil
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}
	rid, err := parseUUID(roleID)
	if err != nil {
		return err
	}
	_, err = c.pool.Exec(ctx, `
		INSERT INTO role_members (role_id, user_id)
		VALUES ($1, $2)
		ON CONFLICT (role_id, user_id) DO UPDATE
		  SET deleted = FALSE, updated_at = NOW()`, rid, uid)
	return err
}

// RemoveUserFromRole soft-deletes the role_members row for the pair.
func (c *Client) RemoveUserFromRole(ctx context.Context, userID, roleID string) error {
	if c == nil {
		return nil
	}
	uid, err := parseUUID(userID)
	if err != nil {
		return err
	}
	rid, err := parseUUID(roleID)
	if err != nil {
		return err
	}
	_, err = c.pool.Exec(ctx, `
		UPDATE role_members SET deleted = TRUE, updated_at = NOW()
		 WHERE role_id = $1 AND user_id = $2`, rid, uid)
	return err
}

// GrantRolePermission adds a (role, permission) row. `relation` is a
// can_* string; it's mapped to the canonical Discord name before the
// insert. Unknown relations error out instead of silently no-oping.
func (c *Client) GrantRolePermission(ctx context.Context, roleID, guildID, relation string) error {
	if c == nil {
		return nil
	}
	name, ok := relationToName[relation]
	if !ok {
		return fmt.Errorf("authz: unknown relation %q", relation)
	}
	rid, err := parseUUID(roleID)
	if err != nil {
		return err
	}
	_, err = c.pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, p.id FROM permissions p WHERE p.name = $2
		ON CONFLICT DO NOTHING`, rid, name)
	return err
}

// RevokeRolePermission deletes the (role, permission) row. No-op if
// the grant never existed.
func (c *Client) RevokeRolePermission(ctx context.Context, roleID, guildID, relation string) error {
	if c == nil {
		return nil
	}
	name, ok := relationToName[relation]
	if !ok {
		return fmt.Errorf("authz: unknown relation %q", relation)
	}
	rid, err := parseUUID(roleID)
	if err != nil {
		return err
	}
	_, err = c.pool.Exec(ctx, `
		DELETE FROM role_permissions
		 WHERE role_id = $1
		   AND permission_id = (SELECT id FROM permissions WHERE name = $2)`, rid, name)
	return err
}

// ListRolePermissions returns the Discord-style permission names granted
// to a role. Useful for role-editor UIs and tests.
func (c *Client) ListRolePermissions(ctx context.Context, roleID string) ([]string, error) {
	if c == nil {
		return nil, nil
	}
	rid, err := parseUUID(roleID)
	if err != nil {
		return nil, err
	}
	rows, err := c.pool.Query(ctx, `
		SELECT p.name FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1
		ORDER BY p.name`, rid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// ── Deprecated no-op stubs ────────────────────────────────────────────
//
// These existed purely to sync OpenFGA tuples. The equivalent state
// now lives in guilds.owner_id, role_members, channels.guild_id — all
// written by the feature services directly. Stubs are kept so callers
// don't need to change; they should be removed in a follow-up pass
// once every call site is audited.

// Deprecated: no-op; guild ownership is derived from guilds.owner_id.
func (c *Client) AddGuildOwner(ctx context.Context, userID, guildID string) error { return nil }

// Deprecated: no-op; guild membership is in guild_members.
func (c *Client) AddGuildMember(ctx context.Context, userID, guildID string) error { return nil }

// Deprecated: no-op; see AddGuildMember.
func (c *Client) RemoveGuildMember(ctx context.Context, userID, guildID string) error { return nil }

// Deprecated: no-op; assign an ADMINISTRATOR permission to a role instead.
func (c *Client) AddGuildAdmin(ctx context.Context, userID, guildID string) error { return nil }

// Deprecated: no-op; see AddGuildAdmin.
func (c *Client) RemoveGuildAdmin(ctx context.Context, userID, guildID string) error { return nil }

// Deprecated: no-op; channel→guild is resolved via channels.guild_id.
func (c *Client) SetChannelGuild(ctx context.Context, channelID, guildID string) error { return nil }

// Deprecated: no-op; role→guild is the FK roles.guild_id.
func (c *Client) BindRoleToGuild(ctx context.Context, roleID, guildID string) error { return nil }

// Deprecated: no-op; see BindRoleToGuild.
func (c *Client) UnbindRoleFromGuild(ctx context.Context, roleID, guildID string) error { return nil }

// Deprecated: no-op; kept so any stragglers still compile.
func (c *Client) WriteTuple(ctx context.Context, user, relation, object string) error { return nil }

// Tuple is retained for API compatibility; WriteTuples is a no-op.
type Tuple struct{ User, Relation, Object string }

// Deprecated: no-op. Grant via role_permissions directly.
func (c *Client) WriteTuples(ctx context.Context, tuples []Tuple) error { return nil }

// Deprecated: no-op.
func (c *Client) DeleteTuple(ctx context.Context, user, relation, object string) error { return nil }

// ── Private utilities ─────────────────────────────────────────────────

func parseUUID(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func uuidToStr(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	// Same canonical form pgtype uses when formatting — matches the
	// string representation stored elsewhere in the codebase.
	s, err := u.Value()
	if err != nil {
		return ""
	}
	str, _ := s.(string)
	return str
}
