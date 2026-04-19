package authz

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	openfga "github.com/openfga/go-sdk/client"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// isDuplicateTupleError reports whether an OpenFGA Write error is the
// expected "tuple already exists" response from an idempotent re-push.
// OpenFGA returns this under `write_failed_due_to_invalid_input` with a
// message mentioning "already existed". Distinguishing it lets us swallow
// duplicates silently (they're normal during startup backfills) while
// still Warn-logging real failures (model mismatch, network, store gone).
func isDuplicateTupleError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "already existed") ||
		strings.Contains(msg, "already exists")
}

// embeddedModel is the OpenFGA authorization model shipped with the binary.
// Source of truth lives at openfga/model.json; this copy guarantees a
// freshly-created store always has a working model without depending on a
// separate init step.
//
//go:embed model.json
var embeddedModel []byte

// Client wraps OpenFGA SDK for authorization checks.
type Client struct {
	fga *openfga.OpenFgaClient
}

func NewClient(cfg config.OpenFGAConfig) (*Client, error) {
	ctx := context.Background()
	storeID := cfg.StoreID

	// Resolve or create the store, then make sure the authorization model
	// is current. Doing this here (rather than in a separate init binary)
	// means a fresh OpenFGA container is usable after a `docker compose up`
	// without any manual bootstrap — role/guild checks stop failing silently
	// with "relation not defined" whenever someone resets the store volume.
	if storeID == "" {
		bootstrap, err := openfga.NewSdkClient(&openfga.ClientConfiguration{ApiUrl: cfg.APIUrl})
		if err != nil {
			return nil, fmt.Errorf("failed to create openfga client: %w", err)
		}
		stores, err := bootstrap.ListStores(ctx).Execute()
		if err != nil {
			return nil, fmt.Errorf("failed to list openfga stores: %w", err)
		}
		for _, s := range stores.GetStores() {
			if s.GetName() == "ndiscord" {
				storeID = s.GetId()
				break
			}
		}
		if storeID == "" {
			created, err := bootstrap.CreateStore(ctx).
				Body(openfga.ClientCreateStoreRequest{Name: "ndiscord"}).
				Execute()
			if err != nil {
				return nil, fmt.Errorf("failed to create openfga store: %w", err)
			}
			storeID = created.GetId()
			logger.Log.Info().Str("store_id", storeID).Msg("openfga: created 'ndiscord' store")
		}
	}

	fgaClient, err := openfga.NewSdkClient(&openfga.ClientConfiguration{
		ApiUrl:  cfg.APIUrl,
		StoreId: storeID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create openfga client: %w", err)
	}

	if err := ensureAuthorizationModel(ctx, fgaClient); err != nil {
		// Non-fatal: without a model, checks will fail and services will
		// return permission-denied rather than crash. Log loudly so ops
		// can fix without chasing ghosts.
		logger.Log.Error().Err(err).Msg("openfga: failed to ensure authorization model — role/guild checks will deny")
	}

	logger.Log.Info().Str("api_url", cfg.APIUrl).Str("store_id", storeID).Msg("connected to openfga")
	return &Client{fga: fgaClient}, nil
}

// ensureAuthorizationModel always pushes the embedded model on startup.
// OpenFGA models are append-only and the latest version is authoritative,
// so re-pushing is safe and cheap — it also means code changes to the
// model (new relations, updated permissions) land automatically on every
// deploy without a separate migration step.
//
// A small optimisation: if the latest stored model already equals the
// embedded one by structural comparison, skip the write to avoid churn
// on Playground / Auditlog. Too much effort for the payoff today; keep
// it simple.
func ensureAuthorizationModel(ctx context.Context, fga *openfga.OpenFgaClient) error {
	var body openfga.ClientWriteAuthorizationModelRequest
	if err := json.Unmarshal(embeddedModel, &body); err != nil {
		return fmt.Errorf("parse embedded model: %w", err)
	}
	res, err := fga.WriteAuthorizationModel(ctx).Body(body).Execute()
	if err != nil {
		return fmt.Errorf("write model: %w", err)
	}
	logger.Log.Info().
		Str("model_id", res.GetAuthorizationModelId()).
		Int("relations", relationCount(embeddedModel)).
		Msg("openfga: pushed embedded authorization model")
	return nil
}

// relationCount is a best-effort count of `can_*` relations for the
// startup log, so ops can see at a glance what surface area the model
// covers today.
func relationCount(raw []byte) int {
	var shape struct {
		TypeDefinitions []struct {
			Relations map[string]any `json:"relations"`
		} `json:"type_definitions"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return 0
	}
	n := 0
	for _, td := range shape.TypeDefinitions {
		n += len(td.Relations)
	}
	return n
}

// WriteTuple creates a relationship tuple. e.g. user:alice is member of guild:123
func (c *Client) WriteTuple(ctx context.Context, user, relation, object string) error {
	if c == nil {
		return nil
	}
	body := openfga.ClientWriteRequest{
		Writes: []openfga.ClientTupleKey{
			{User: user, Relation: relation, Object: object},
		},
	}
	_, err := c.fga.Write(ctx).Body(body).Execute()
	if err != nil {
		// Duplicate tuples are expected on idempotent retries — swallow
		// silently so startup syncs don't spam. Real failures still log.
		if isDuplicateTupleError(err) {
			return nil
		}
		logger.Log.Warn().Err(err).Str("user", user).Str("relation", relation).Str("object", object).Msg("openfga write failed")
		return nil
	}
	return nil
}

// Tuple is a (user, relation, object) triple suitable for batch writes.
type Tuple struct {
	User, Relation, Object string
}

// WriteTuples pushes up to ~100 tuples in a single HTTP call. OpenFGA
// rejects a batch if ANY tuple already exists, so on conflict we fall
// back to one-by-one writes where duplicates are silently swallowed.
// Used by startup backfill sync paths to avoid N round trips.
func (c *Client) WriteTuples(ctx context.Context, tuples []Tuple) error {
	if c == nil || len(tuples) == 0 {
		return nil
	}
	const batchSize = 100
	for i := 0; i < len(tuples); i += batchSize {
		end := i + batchSize
		if end > len(tuples) {
			end = len(tuples)
		}
		chunk := tuples[i:end]
		keys := make([]openfga.ClientTupleKey, len(chunk))
		for j, t := range chunk {
			keys[j] = openfga.ClientTupleKey{User: t.User, Relation: t.Relation, Object: t.Object}
		}
		_, err := c.fga.Write(ctx).Body(openfga.ClientWriteRequest{Writes: keys}).Execute()
		if err == nil {
			continue
		}
		if !isDuplicateTupleError(err) {
			logger.Log.Warn().Err(err).Int("batch", len(chunk)).Msg("openfga batch write failed, falling back to per-tuple")
		}
		// Batch rejected (at least one duplicate) — retry per-tuple.
		// The single-tuple WriteTuple swallows duplicates silently so
		// only genuinely new tuples land and errors surface properly.
		for _, t := range chunk {
			_ = c.WriteTuple(ctx, t.User, t.Relation, t.Object)
		}
	}
	return nil
}

// DeleteTuple removes a relationship tuple.
func (c *Client) DeleteTuple(ctx context.Context, user, relation, object string) error {
	if c == nil {
		return nil
	}
	body := openfga.ClientWriteRequest{
		Deletes: []openfga.ClientTupleKeyWithoutCondition{
			{User: user, Relation: relation, Object: object},
		},
	}
	_, err := c.fga.Write(ctx).Body(body).Execute()
	if err != nil {
		// "tuple to be deleted did not exist" is the delete-side
		// equivalent of a duplicate on write — swallow silently.
		if strings.Contains(err.Error(), "did not exist") {
			logger.Log.Debug().Str("user", user).Str("relation", relation).Str("object", object).Msg("openfga delete skipped — tuple absent")
			return nil
		}
		logger.Log.Warn().Err(err).Str("user", user).Str("relation", relation).Str("object", object).Msg("openfga delete failed")
	}
	return nil
}

// Check returns true if the user has the given relation on the object.
func (c *Client) Check(ctx context.Context, user, relation, object string) bool {
	if c == nil {
		return true // no authz = allow all (for tests without OpenFGA)
	}
	body := openfga.ClientCheckRequest{
		User:     user,
		Relation: relation,
		Object:   object,
	}
	resp, err := c.fga.Check(ctx).Body(body).Execute()
	if err != nil {
		logger.Log.Error().Err(err).Str("user", user).Str("relation", relation).Str("object", object).Msg("openfga check failed")
		return false
	}
	return resp.GetAllowed()
}

// === Convenience methods for ndiscord ===

func UserKey(userID string) string       { return "user:" + userID }
func GuildKey(guildID string) string     { return "guild:" + guildID }
func ChannelKey(channelID string) string { return "channel:" + channelID }
func RoleKey(roleID string) string       { return "role:" + roleID }

// RoleMembersKey is the userset "members of this role" — used as the
// left-hand side when granting a permission to everyone in the role.
// Matches OpenFGA's `role:R#member` syntax.
func RoleMembersKey(roleID string) string { return "role:" + roleID + "#member" }

// Guild membership
func (c *Client) AddGuildOwner(ctx context.Context, userID, guildID string) error {
	return c.WriteTuple(ctx, UserKey(userID), "owner", GuildKey(guildID))
}

func (c *Client) AddGuildMember(ctx context.Context, userID, guildID string) error {
	return c.WriteTuple(ctx, UserKey(userID), "member", GuildKey(guildID))
}

func (c *Client) RemoveGuildMember(ctx context.Context, userID, guildID string) error {
	return c.DeleteTuple(ctx, UserKey(userID), "member", GuildKey(guildID))
}

func (c *Client) AddGuildAdmin(ctx context.Context, userID, guildID string) error {
	return c.WriteTuple(ctx, UserKey(userID), "admin", GuildKey(guildID))
}

func (c *Client) RemoveGuildAdmin(ctx context.Context, userID, guildID string) error {
	return c.DeleteTuple(ctx, UserKey(userID), "admin", GuildKey(guildID))
}

// Channel → Guild relationship
func (c *Client) SetChannelGuild(ctx context.Context, channelID, guildID string) error {
	return c.WriteTuple(ctx, GuildKey(guildID), "guild", ChannelKey(channelID))
}

// === Roles ===

// BindRoleToGuild records that a role belongs to a guild. Called on
// role create; the tuple lets us clean up role-scoped permission grants
// later and keeps OpenFGA aware of which guild "owns" the role.
func (c *Client) BindRoleToGuild(ctx context.Context, roleID, guildID string) error {
	return c.WriteTuple(ctx, GuildKey(guildID), "guild", RoleKey(roleID))
}

// UnbindRoleFromGuild removes the role→guild link. Called on role delete.
func (c *Client) UnbindRoleFromGuild(ctx context.Context, roleID, guildID string) error {
	return c.DeleteTuple(ctx, GuildKey(guildID), "guild", RoleKey(roleID))
}

// AddUserToRole / RemoveUserFromRole — assign or un-assign a user to a role.
// This is the Discord "member has role" relationship; permissions flow
// from role → user via the `role#member` userset on guild can_* relations.
func (c *Client) AddUserToRole(ctx context.Context, userID, roleID string) error {
	return c.WriteTuple(ctx, UserKey(userID), "member", RoleKey(roleID))
}

func (c *Client) RemoveUserFromRole(ctx context.Context, userID, roleID string) error {
	return c.DeleteTuple(ctx, UserKey(userID), "member", RoleKey(roleID))
}

// GrantRolePermission grants a single permission to everyone who is a
// member of the given role. `relation` is one of the `can_*` strings
// from PermissionRelation. On revoke, pass the same arguments to
// RevokeRolePermission.
func (c *Client) GrantRolePermission(ctx context.Context, roleID, guildID, relation string) error {
	return c.WriteTuple(ctx, RoleMembersKey(roleID), relation, GuildKey(guildID))
}

func (c *Client) RevokeRolePermission(ctx context.Context, roleID, guildID, relation string) error {
	return c.DeleteTuple(ctx, RoleMembersKey(roleID), relation, GuildKey(guildID))
}

// Permission checks
// CanGuildPermission is the generic Check helper for any guild-scoped
// permission. `relation` is one of the `can_*` relations defined in
// openfga/model.json (see also the PermissionRelation map below).
// Prefer the typed wrappers right below this for the common cases.
func (c *Client) CanGuildPermission(ctx context.Context, userID, guildID, relation string) bool {
	return c.Check(ctx, UserKey(userID), relation, GuildKey(guildID))
}

// PermissionRelation maps Discord-style permission names (matching the
// Permission enum values in guild.proto) to the OpenFGA relation they
// resolve to. Used by RPC handlers that accept a Permission enum from
// the wire and need to run a Check on it.
var PermissionRelation = map[string]string{
	// Existing
	"VIEW_CHANNELS":    "can_view_channels",
	"SEND_MESSAGES":    "can_send_messages",
	"MANAGE_MESSAGES":  "can_manage_messages",
	"MANAGE_CHANNELS":  "can_manage_channels",
	"MANAGE_GUILD":     "can_manage_guild",
	"KICK_MEMBERS":     "can_kick",
	"BAN_MEMBERS":      "can_ban",
	"MANAGE_ROLES":     "can_manage_roles",
	"MANAGE_INVITES":   "can_manage_invites",
	"ADD_REACTIONS":    "can_add_reactions",
	"CONNECT":          "can_connect",
	"SPEAK":            "can_speak",
	"STREAM":           "can_stream",
	"MUTE_MEMBERS":     "can_mute_members",
	"DEAFEN_MEMBERS":   "can_deafen_members",
	"MENTION_EVERYONE": "can_mention_everyone",
	"MANAGE_EMOJIS":    "can_manage_emojis",
	"MANAGE_WEBHOOKS":  "can_manage_webhooks",

	// Additions
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
	// ADMINISTRATOR → check the `admin` direct relation; no can_* wrapper
	"ADMINISTRATOR": "admin",
}

// Short named helpers for the checks that the codebase calls most often.
// For anything not listed here, use CanGuildPermission(ctx, u, g, relation)
// with a relation string from PermissionRelation.

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

// Fine-grained guild-scoped permission helpers used by services to gate
// specific actions. Each mirrors a Discord permission flag.
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

func (c *Client) CanViewChannel(ctx context.Context, userID, channelID string) bool {
	return c.Check(ctx, UserKey(userID), "viewer", ChannelKey(channelID))
}

func (c *Client) CanSendInChannel(ctx context.Context, userID, channelID string) bool {
	return c.Check(ctx, UserKey(userID), "sender", ChannelKey(channelID))
}

func (c *Client) CanManageChannel(ctx context.Context, userID, channelID string) bool {
	return c.Check(ctx, UserKey(userID), "manager", ChannelKey(channelID))
}
