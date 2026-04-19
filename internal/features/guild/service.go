package guild

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/minio/minio-go/v7"

	"github.com/google/uuid"

	"github.com/ananddub/ndiscord_backend/gen/db"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

type Service struct {
	repo          *Repository
	authz         *authz.Client
	nats          *realtime.Hub
	minio         *minio.Client
	signer        *minio.Client
	bucket        string
	inviteBaseURL string
}

// SetInviteBaseURL sets the public base URL used to build shareable invite links.
func (s *Service) SetInviteBaseURL(base string) {
	s.inviteBaseURL = base
}

// BuildInviteURL returns the shareable URL for an invite code.
func (s *Service) BuildInviteURL(code string) string {
	if s.inviteBaseURL == "" || code == "" {
		return ""
	}
	return s.inviteBaseURL + "/" + code
}

func NewService(repo *Repository, nats *realtime.Hub, authzClient ...*authz.Client) *Service {
	s := &Service{repo: repo, nats: nats}
	if len(authzClient) > 0 {
		s.authz = authzClient[0]
	}
	return s
}

// SetStorage wires the MinIO clients used for guild icon uploads.
// `client` is the internal client used to upload bytes; `signer` is the
// public-endpoint client used to generate presigned URLs for clients.
func (s *Service) SetStorage(client, signer *minio.Client, bucket string) {
	s.minio = client
	s.signer = signer
	s.bucket = bucket
}

// ResolveIconURL generates a fresh presigned GET URL for a stored object key.
// If the key is empty it returns empty string. If the key looks like a full URL
// (legacy data), it is returned as-is.
func (s *Service) ResolveIconURL(ctx context.Context, key string) string {
	return s.resolveGuildAssetURL(ctx, key)
}

// ResolveBannerURL mirrors ResolveIconURL for the guild background image.
// Banners share the same object-storage layout (different key prefix) so the
// same presigner works.
func (s *Service) ResolveBannerURL(ctx context.Context, key string) string {
	return s.resolveGuildAssetURL(ctx, key)
}

func (s *Service) resolveGuildAssetURL(ctx context.Context, key string) string {
	if key == "" {
		return ""
	}
	if len(key) >= 7 && (key[:7] == "http://" || (len(key) >= 8 && key[:8] == "https://")) {
		return key
	}
	if s.signer == nil {
		return ""
	}
	u, err := s.signer.PresignedGetObject(ctx, s.bucket, key, guildIconDownloadTTL, url.Values{})
	if err != nil {
		return ""
	}
	return u.String()
}

const (
	maxGuildIconSize     = 5 * 1024 * 1024
	maxGuildBannerSize   = 10 * 1024 * 1024 // banners are bigger; match proto validator
	guildIconDownloadTTL = 7 * 24 * time.Hour
)

var allowedIconContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/gif":  true,
	"image/webp": true,
}

// UploadGuildIcon uploads raw icon bytes to object storage and updates the guild's icon_url.
func (s *Service) UploadGuildIcon(ctx context.Context, callerID, guildID pgtype.UUID, filename, contentType string, data []byte) (db.Guild, string, error) {
	if s.minio == nil {
		return db.Guild{}, "", fmt.Errorf("storage not configured")
	}
	if !s.authz.CanManageGuild(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return db.Guild{}, "", ErrInsufficientPermissions
	}
	if len(data) == 0 {
		return db.Guild{}, "", fmt.Errorf("empty file")
	}
	if len(data) > maxGuildIconSize {
		return db.Guild{}, "", fmt.Errorf("icon too large (max %d bytes)", maxGuildIconSize)
	}
	if !allowedIconContentTypes[contentType] {
		return db.Guild{}, "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	objectKey := fmt.Sprintf("guilds/%s/icon_%s_%s", pgToStr(guildID), uuid.New().String(), filename)

	_, err := s.minio.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return db.Guild{}, "", fmt.Errorf("failed to upload icon: %w", err)
	}

	// Store only the object key; a fresh presigned URL is generated on every
	// read via ResolveIconURL so clients always get a working, public-facing URL.
	updated, err := s.repo.UpdateGuild(ctx, db.UpdateGuildParams{
		ID:      guildID,
		IconUrl: &objectKey,
	})
	if err != nil {
		return db.Guild{}, "", fmt.Errorf("failed to update guild icon: %w", err)
	}

	iconURL := s.ResolveIconURL(ctx, objectKey)
	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_UPDATE, map[string]string{"icon_url": iconURL})

	return updated, iconURL, nil
}

// UploadGuildBanner uploads raw banner bytes (hero background image) to object
// storage and updates the guild's banner_url. Mirrors UploadGuildIcon; the
// size cap is larger because banners are typically 1920-wide cover images.
func (s *Service) UploadGuildBanner(ctx context.Context, callerID, guildID pgtype.UUID, filename, contentType string, data []byte) (db.Guild, string, error) {
	if s.minio == nil {
		return db.Guild{}, "", fmt.Errorf("storage not configured")
	}
	if !s.authz.CanManageGuild(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return db.Guild{}, "", ErrInsufficientPermissions
	}
	if len(data) == 0 {
		return db.Guild{}, "", fmt.Errorf("empty file")
	}
	if len(data) > maxGuildBannerSize {
		return db.Guild{}, "", fmt.Errorf("banner too large (max %d bytes)", maxGuildBannerSize)
	}
	if !allowedIconContentTypes[contentType] {
		return db.Guild{}, "", fmt.Errorf("unsupported content type: %s", contentType)
	}

	objectKey := fmt.Sprintf("guilds/%s/banner_%s_%s", pgToStr(guildID), uuid.New().String(), filename)

	_, err := s.minio.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return db.Guild{}, "", fmt.Errorf("failed to upload banner: %w", err)
	}

	updated, err := s.repo.UpdateGuild(ctx, db.UpdateGuildParams{
		ID:        guildID,
		BannerUrl: &objectKey,
	})
	if err != nil {
		return db.Guild{}, "", fmt.Errorf("failed to update guild banner: %w", err)
	}

	bannerURL := s.ResolveBannerURL(ctx, objectKey)
	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_UPDATE, map[string]string{"banner_url": bannerURL})

	return updated, bannerURL, nil
}

// pgToStr converts a pgtype.UUID to its string representation for authz calls.
func pgToStr(id pgtype.UUID) string {
	return uuid.UUID(id.Bytes).String()
}

func (s *Service) publishGuildEvent(ctx context.Context, guildID pgtype.UUID, action streamv1.GuildEventType, extra map[string]string) {
	gid := pgToStr(guildID)
	evt := &streamv1.GuildEvent{
		Event:   action,
		Action:  action,
		GuildId: gid,
	}
	// Map well-known extras into typed fields.
	for k, v := range extra {
		switch k {
		case "user_id":
			evt.UserId = v
		case "channel_id":
			evt.ChannelId = v
		case "name":
			evt.Name = v
		case "icon_url":
			evt.IconUrl = v
		case "banner_url":
			evt.BannerUrl = v
		case "role_id":
			evt.RoleId = v
		case "reason":
			evt.Reason = v
		case "parent_id":
			evt.ParentId = v
		case "topic":
			evt.Topic = v
		}
	}
	_ = s.nats.Publish(realtime.GuildEvents(gid), evt)
}

func (s *Service) CreateGuild(ctx context.Context, ownerID pgtype.UUID, name, description, iconURL string) (db.Guild, error) {
	guild, err := s.repo.CreateGuild(ctx, db.CreateGuildParams{
		Name:        name,
		Description: description,
		IconUrl:     iconURL,
		OwnerID:     ownerID,
	})
	if err != nil {
		return db.Guild{}, fmt.Errorf("failed to create guild: %w", err)
	}

	// Add the owner as a member
	_, err = s.repo.AddGuildMember(ctx, db.AddGuildMemberParams{
		GuildID:  guild.ID,
		UserID:   ownerID,
		Nickname: "",
	})
	if err != nil {
		return db.Guild{}, fmt.Errorf("failed to add owner as member: %w", err)
	}

	// Write authz tuples
	ownerStr := pgToStr(ownerID)
	guildStr := pgToStr(guild.ID)
	_ = s.authz.AddGuildOwner(ctx, ownerStr, guildStr)
	_ = s.authz.AddGuildMember(ctx, ownerStr, guildStr)

	// Auto-create the @everyone role — Discord convention: every guild
	// has an implicit role every member belongs to. Permission grants on
	// it form the guild's baseline (e.g. muted-by-default).
	_ = s.ensureEveryoneRole(ctx, guild.ID, ownerID)

	s.publishGuildEvent(ctx, guild.ID, streamv1.GuildEventType_GUILD_EVENT_MEMBER_ADD, map[string]string{
		"user_id": ownerStr, "name": name,
	})

	return guild, nil
}

// ensureEveryoneRole creates the @everyone row if it doesn't exist and
// assigns the given user to it. Idempotent on both the Postgres row
// (name+guild UNIQUE via ON CONFLICT isn't set today, so we GET before
// INSERT) and the OpenFGA tuples (duplicates are swallowed silently).
// A failure here is best-effort: the guild / member creation succeeded,
// and the next member add will also attempt to provision the role.
func (s *Service) ensureEveryoneRole(ctx context.Context, guildID, userID pgtype.UUID) error {
	role, err := s.repo.GetEveryoneRole(ctx, guildID)
	if err != nil {
		role, err = s.repo.CreateRole(ctx, db.CreateRoleParams{
			GuildID:  guildID,
			Name:     "@everyone",
			Color:    "",
			Position: 0,
		})
		if err != nil {
			return fmt.Errorf("create @everyone role: %w", err)
		}
		// Three tuples together wire up the @everyone inheritance chain:
		//   1. role → guild       — the role "belongs to" this guild
		//   2. role#member → guild member — every role member is a guild member,
		//      which cascades into every baseline can_* relation via the
		//      union on "member" defined in the model.
		// (3) the per-user member→role tuple is written below.
		_ = s.authz.BindRoleToGuild(ctx, pgToStr(role.ID), pgToStr(guildID))
		_ = s.authz.WriteTuple(ctx, authz.RoleMembersKey(pgToStr(role.ID)), "member", authz.GuildKey(pgToStr(guildID)))
	}
	// Assign the user to @everyone in both Postgres and OpenFGA.
	_ = s.repo.AssignRole(ctx, db.AssignRoleParams{RoleID: role.ID, UserID: userID})
	_ = s.authz.AddUserToRole(ctx, pgToStr(userID), pgToStr(role.ID))
	return nil
}

// assignEveryoneRole is the lightweight variant used on every member
// add — assumes the role already exists (guild was created via
// CreateGuild, which provisions it). Falls back to ensureEveryoneRole
// if the role is missing (e.g. historical rows from before this feature).
func (s *Service) assignEveryoneRole(ctx context.Context, guildID, userID pgtype.UUID) {
	role, err := s.repo.GetEveryoneRole(ctx, guildID)
	if err != nil {
		_ = s.ensureEveryoneRole(ctx, guildID, userID)
		return
	}
	_ = s.repo.AssignRole(ctx, db.AssignRoleParams{RoleID: role.ID, UserID: userID})
	_ = s.authz.AddUserToRole(ctx, pgToStr(userID), pgToStr(role.ID))
}

// removeEveryoneRole un-assigns a user from the @everyone role, used on
// kick/ban/leave paths. Idempotent.
func (s *Service) removeEveryoneRole(ctx context.Context, guildID, userID pgtype.UUID) {
	role, err := s.repo.GetEveryoneRole(ctx, guildID)
	if err != nil {
		return
	}
	_ = s.repo.RemoveRole(ctx, db.RemoveRoleParams{RoleID: role.ID, UserID: userID})
	_ = s.authz.RemoveUserFromRole(ctx, pgToStr(userID), pgToStr(role.ID))
}

// syncVersion is bumped when the sync logic / model needs to re-run
// against existing data (e.g. a new relation added to the authz model).
// The current value is recorded in Redis after a successful sync so
// subsequent boots are no-ops.
const syncVersion = "v2-roles-channels"

// syncMarkerKey is the Redis key that records the last completed sync
// version. Stored per-server; single Redis cluster = single source of
// truth across replicas.
const syncMarkerKey = "authz:sync:version"

// RunOneTimeBackfillIfNeeded runs both backfill sync steps exactly once
// per sync-version per Redis dataset. Idempotent re-pushes are still
// safe (duplicates are silently swallowed) but avoiding the DB reads
// and HTTP round trips on every boot keeps startup fast and logs clean.
// Bump syncVersion when a schema change requires replaying the sync.
func (s *Service) RunOneTimeBackfillIfNeeded(ctx context.Context) {
	if s.repo.redis != nil {
		current, _ := s.repo.redis.Get(ctx, syncMarkerKey).Result()
		if current == syncVersion {
			return
		}
	}
	if err := s.SyncEveryoneRoles(ctx); err != nil {
		// Don't stamp the marker — retry on next boot.
		return
	}
	if err := s.SyncChannelGuildTuples(ctx); err != nil {
		return
	}
	if s.repo.redis != nil {
		_ = s.repo.redis.Set(ctx, syncMarkerKey, syncVersion, 0).Err()
	}
}

// SyncChannelGuildTuples pushes `channel --guild--> guild` tuples for
// every guild channel that exists in Postgres. CreateChannel already
// writes this tuple on the hot path; this sync covers channels that
// pre-date OpenFGA integration (or were created while OpenFGA was down).
// Idempotent — duplicate tuples are swallowed by the authz client.
func (s *Service) SyncChannelGuildTuples(ctx context.Context) error {
	rows, err := s.repo.ListAllGuildChannels(ctx)
	if err != nil {
		return fmt.Errorf("list channels: %w", err)
	}
	tuples := make([]authz.Tuple, len(rows))
	for i, row := range rows {
		tuples[i] = authz.Tuple{
			User:     authz.GuildKey(pgToStr(row.GuildID)),
			Relation: "guild",
			Object:   authz.ChannelKey(pgToStr(row.ID)),
		}
	}
	return s.authz.WriteTuples(ctx, tuples)
}

// SyncEveryoneRoles reads every @everyone role assignment from Postgres
// (the source of truth after migration 000010) and pushes the matching
// OpenFGA tuples. Both the guild→role binding and user→role membership
// are written. Idempotent: duplicate-tuple errors are swallowed by the
// authz client, so calling this on every startup is safe and cheap.
//
// Runs best-effort; errors are logged but never fatal because a stale
// OpenFGA copy still degrades to per-user direct grants.
func (s *Service) SyncEveryoneRoles(ctx context.Context) error {
	rows, err := s.repo.ListEveryoneAssignments(ctx)
	if err != nil {
		return fmt.Errorf("list @everyone assignments: %w", err)
	}
	seenRoles := make(map[string]struct{}, len(rows))
	tuples := make([]authz.Tuple, 0, len(rows)*2)
	for _, row := range rows {
		guildStr := pgToStr(row.GuildID)
		roleStr := pgToStr(row.RoleID)
		userStr := pgToStr(row.UserID)
		if _, ok := seenRoles[roleStr]; !ok {
			tuples = append(tuples,
				// role -> guild: which guild this role belongs to
				authz.Tuple{User: authz.GuildKey(guildStr), Relation: "guild", Object: authz.RoleKey(roleStr)},
				// role#member -> member -> guild: role population cascades
				// into guild members so baseline can_* resolves per-user.
				authz.Tuple{User: authz.RoleMembersKey(roleStr), Relation: "member", Object: authz.GuildKey(guildStr)},
			)
			seenRoles[roleStr] = struct{}{}
		}
		tuples = append(tuples, authz.Tuple{User: authz.UserKey(userStr), Relation: "member", Object: authz.RoleKey(roleStr)})
	}
	return s.authz.WriteTuples(ctx, tuples)
}

func (s *Service) GetGuild(ctx context.Context, guildID pgtype.UUID) (db.Guild, int64, error) {
	guild, err := s.repo.GetGuildByID(ctx, guildID)
	if err != nil {
		return db.Guild{}, 0, fmt.Errorf("%w: %v", ErrGuildNotFound, err)
	}

	count, err := s.repo.CountGuildMembers(ctx, guildID)
	if err != nil {
		return db.Guild{}, 0, fmt.Errorf("failed to count members: %w", err)
	}

	return guild, count, nil
}

func (s *Service) UpdateGuild(ctx context.Context, callerID, guildID pgtype.UUID, params db.UpdateGuildParams) (db.Guild, error) {
	if !s.authz.CanManageGuild(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return db.Guild{}, ErrInsufficientPermissions
	}

	params.ID = guildID
	updated, err := s.repo.UpdateGuild(ctx, params)
	if err != nil {
		return db.Guild{}, fmt.Errorf("failed to update guild: %w", err)
	}

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_UPDATE, map[string]string{"name": updated.Name})

	return updated, nil
}

func (s *Service) DeleteGuild(ctx context.Context, callerID, guildID pgtype.UUID) error {
	// Only owner can delete guild (not even admins)
	guild, err := s.repo.GetGuildByID(ctx, guildID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGuildNotFound, err)
	}
	if guild.OwnerID != callerID {
		return ErrNotGuildOwner
	}

	if err := s.repo.DeleteGuild(ctx, guildID); err != nil {
		return fmt.Errorf("failed to delete guild: %w", err)
	}

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_DELETE, nil)

	return nil
}

func (s *Service) ListUserGuilds(ctx context.Context, userID pgtype.UUID) ([]db.Guild, error) {
	guilds, err := s.repo.ListUserGuilds(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list guilds: %w", err)
	}
	return guilds, nil
}

// JoinGuildResult holds the full payload returned after joining via invite.
type JoinGuildResult struct {
	Guild       db.Guild
	Channels    []db.Channel
	MemberCount int64
}

// PreviewInviteResult holds guild metadata shown before accepting an invite.
type PreviewInviteResult struct {
	Invite          db.Invite
	Guild           db.Guild
	MemberCount     int64
	ChannelCount    int32
	InviterUsername string
	AlreadyMember   bool
}

// PreviewInvite returns guild metadata for an invite code without joining.
// It performs the same validity checks as JoinGuild (expired, exhausted, banned)
// so the caller knows up-front whether the invite is usable.
func (s *Service) PreviewInvite(ctx context.Context, callerID pgtype.UUID, inviteCode string) (*PreviewInviteResult, error) {
	invite, err := s.repo.GetInvite(ctx, inviteCode)
	if err != nil {
		return nil, ErrInviteNotFound
	}

	if invite.ExpiresAt.Valid && invite.ExpiresAt.Time.Before(time.Now()) {
		return nil, ErrInviteExpired
	}
	if invite.MaxUses > 0 && invite.Uses >= invite.MaxUses {
		return nil, ErrInviteMaxUses
	}

	if callerID.Valid {
		if _, banErr := s.repo.GetBan(ctx, db.GetBanParams{
			GuildID: invite.GuildID,
			UserID:  callerID,
		}); banErr == nil {
			return nil, ErrUserBanned
		}
	}

	guild, err := s.repo.GetGuildByID(ctx, invite.GuildID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrGuildNotFound, err)
	}

	memberCount, err := s.repo.CountGuildMembers(ctx, invite.GuildID)
	if err != nil {
		return nil, fmt.Errorf("failed to count members: %w", err)
	}

	channels, err := s.repo.ListGuildChannels(ctx, invite.GuildID)
	if err != nil {
		return nil, fmt.Errorf("failed to list channels: %w", err)
	}

	inviterUsername := ""
	if inviter, err := s.repo.GetUserByID(ctx, invite.CreatorID); err == nil {
		inviterUsername = inviter.Username
	}

	alreadyMember := false
	if callerID.Valid {
		if _, memErr := s.repo.GetGuildMember(ctx, db.GetGuildMemberParams{
			GuildID: invite.GuildID,
			UserID:  callerID,
		}); memErr == nil {
			alreadyMember = true
		}
	}

	return &PreviewInviteResult{
		Invite:          invite,
		Guild:           guild,
		MemberCount:     memberCount,
		ChannelCount:    int32(len(channels)),
		InviterUsername: inviterUsername,
		AlreadyMember:   alreadyMember,
	}, nil
}

func (s *Service) JoinGuild(ctx context.Context, userID pgtype.UUID, inviteCode string) (*JoinGuildResult, error) {
	invite, err := s.repo.GetInvite(ctx, inviteCode)
	if err != nil {
		return nil, ErrInviteNotFound
	}

	// Check expiry
	if invite.ExpiresAt.Valid && invite.ExpiresAt.Time.Before(time.Now()) {
		return nil, ErrInviteExpired
	}

	// Check max uses
	if invite.MaxUses > 0 && invite.Uses >= invite.MaxUses {
		return nil, ErrInviteMaxUses
	}

	// Check if banned
	_, err = s.repo.GetBan(ctx, db.GetBanParams{
		GuildID: invite.GuildID,
		UserID:  userID,
	})
	if err == nil {
		return nil, ErrUserBanned
	}

	// Idempotent: if already a member, return guild data so double-taps
	// and retries don't show an error to the user.
	_, err = s.repo.GetGuildMember(ctx, db.GetGuildMemberParams{
		GuildID: invite.GuildID,
		UserID:  userID,
	})
	if err == nil {
		return s.buildJoinResult(ctx, invite.GuildID)
	}

	// Add member with invite tracking — records which invite code was used
	// and who created that invite so the guild owner can audit joins.
	_, err = s.repo.AddGuildMember(ctx, db.AddGuildMemberParams{
		GuildID:    invite.GuildID,
		UserID:     userID,
		Nickname:   "",
		InviteCode: &inviteCode,
		InvitedBy:  invite.CreatorID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to add guild member: %w", err)
	}

	// Increment invite uses counter
	_ = s.repo.IncrementInviteUses(ctx, inviteCode)

	// Append detailed invite usage log
	_, _ = s.repo.RecordInviteUse(ctx, db.RecordInviteUseParams{
		InviteCode: inviteCode,
		GuildID:    invite.GuildID,
		UserID:     userID,
		InviterID:  invite.CreatorID,
	})

	// Write authz tuple
	userStr := pgToStr(userID)
	guildStr := pgToStr(invite.GuildID)
	_ = s.authz.AddGuildMember(ctx, userStr, guildStr)
	s.assignEveryoneRole(ctx, invite.GuildID, userID)

	s.publishGuildEvent(ctx, invite.GuildID, streamv1.GuildEventType_GUILD_EVENT_MEMBER_ADD, map[string]string{
		"user_id": userStr,
	})

	return s.buildJoinResult(ctx, invite.GuildID)
}

// buildJoinResult loads guild + channels + member count for the JoinGuild
// response. Reused for both fresh joins and already-member short-circuits.
func (s *Service) buildJoinResult(ctx context.Context, guildID pgtype.UUID) (*JoinGuildResult, error) {
	guild, err := s.repo.GetGuildByID(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to get guild: %w", err)
	}
	channels, err := s.repo.ListGuildChannels(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to list channels: %w", err)
	}
	memberCount, err := s.repo.CountGuildMembers(ctx, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to count members: %w", err)
	}
	return &JoinGuildResult{
		Guild:       guild,
		Channels:    channels,
		MemberCount: memberCount,
	}, nil
}

func (s *Service) LeaveGuild(ctx context.Context, userID, guildID pgtype.UUID) error {
	guild, err := s.repo.GetGuildByID(ctx, guildID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGuildNotFound, err)
	}
	if guild.OwnerID == userID {
		return ErrOwnerCannotLeave
	}

	_, err = s.repo.GetGuildMember(ctx, db.GetGuildMemberParams{
		GuildID: guildID,
		UserID:  userID,
	})
	if err != nil {
		return ErrNotGuildMember
	}

	if err = s.repo.RemoveGuildMember(ctx, db.RemoveGuildMemberParams{
		GuildID: guildID,
		UserID:  userID,
	}); err != nil {
		return err
	}

	// Remove authz tuple + drop from @everyone role
	userStr := pgToStr(userID)
	guildStr := pgToStr(guildID)
	_ = s.authz.RemoveGuildMember(ctx, userStr, guildStr)
	s.removeEveryoneRole(ctx, guildID, userID)

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_MEMBER_REMOVE, map[string]string{
		"user_id": userStr,
	})

	return nil
}

func (s *Service) ListMembers(ctx context.Context, guildID pgtype.UUID, limit, offset int32) ([]db.GuildMember, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.repo.ListGuildMembers(ctx, db.ListGuildMembersParams{
		GuildID: guildID,
		Limit:   limit,
		Offset:  offset,
	})
}

func (s *Service) KickMember(ctx context.Context, callerID, guildID, targetID pgtype.UUID) error {
	if !s.authz.CanKick(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return ErrInsufficientPermissions
	}
	guild, err := s.repo.GetGuildByID(ctx, guildID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGuildNotFound, err)
	}
	if guild.OwnerID == targetID {
		return ErrCannotKickOwner
	}

	_, err = s.repo.GetGuildMember(ctx, db.GetGuildMemberParams{
		GuildID: guildID,
		UserID:  targetID,
	})
	if err != nil {
		return ErrMemberNotFound
	}

	if err = s.repo.RemoveGuildMember(ctx, db.RemoveGuildMemberParams{
		GuildID: guildID,
		UserID:  targetID,
	}); err != nil {
		return err
	}

	// Remove authz tuple + drop from @everyone role
	targetStr := pgToStr(targetID)
	guildStr := pgToStr(guildID)
	_ = s.authz.RemoveGuildMember(ctx, targetStr, guildStr)
	s.removeEveryoneRole(ctx, guildID, targetID)

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_MEMBER_REMOVE, map[string]string{
		"user_id": targetStr,
	})

	return nil
}

func (s *Service) BanMember(ctx context.Context, callerID, guildID, targetID pgtype.UUID, reason string) error {
	if !s.authz.CanBan(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return ErrInsufficientPermissions
	}
	guild, err := s.repo.GetGuildByID(ctx, guildID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrGuildNotFound, err)
	}
	if guild.OwnerID == targetID {
		return ErrCannotBanOwner
	}

	// Remove from guild if member
	_ = s.repo.RemoveGuildMember(ctx, db.RemoveGuildMemberParams{
		GuildID: guildID,
		UserID:  targetID,
	})

	_, err = s.repo.CreateBan(ctx, db.CreateBanParams{
		GuildID: guildID,
		UserID:  targetID,
		Reason:  reason,
	})
	if err != nil {
		return fmt.Errorf("failed to create ban: %w", err)
	}

	// Remove authz tuple + drop from @everyone role
	targetStr := pgToStr(targetID)
	guildStr := pgToStr(guildID)
	_ = s.authz.RemoveGuildMember(ctx, targetStr, guildStr)
	s.removeEveryoneRole(ctx, guildID, targetID)

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_MEMBER_BAN, map[string]string{
		"user_id": targetStr,
		"reason":  reason,
	})

	return nil
}

func (s *Service) UnbanMember(ctx context.Context, callerID, guildID, targetID pgtype.UUID) error {
	if !s.authz.CanBan(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return ErrInsufficientPermissions
	}

	_, banErr := s.repo.GetBan(ctx, db.GetBanParams{
		GuildID: guildID,
		UserID:  targetID,
	})
	if banErr != nil {
		return ErrUserNotBanned
	}

	if err := s.repo.DeleteBan(ctx, db.DeleteBanParams{
		GuildID: guildID,
		UserID:  targetID,
	}); err != nil {
		return err
	}

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_MEMBER_UNBAN, map[string]string{
		"user_id": pgToStr(targetID),
	})

	return nil
}

func (s *Service) CreateInvite(ctx context.Context, callerID, guildID, channelID pgtype.UUID, maxUses, maxAgeSeconds int32) (db.Invite, error) {
	// Verify caller is a member
	_, err := s.repo.GetGuildMember(ctx, db.GetGuildMemberParams{
		GuildID: guildID,
		UserID:  callerID,
	})
	if err != nil {
		return db.Invite{}, ErrNotGuildMember
	}

	// MANAGE_INVITES (aka Discord's CREATE_INSTANT_INVITE) gate.
	if !s.authz.CanManageInvites(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return db.Invite{}, ErrInsufficientPermissions
	}

	code, err := generateInviteCode()
	if err != nil {
		return db.Invite{}, fmt.Errorf("failed to generate invite code: %w", err)
	}

	var expiresAt pgtype.Timestamptz
	if maxAgeSeconds > 0 {
		expiresAt = pgtype.Timestamptz{
			Time:  time.Now().Add(time.Duration(maxAgeSeconds) * time.Second),
			Valid: true,
		}
	}

	invite, err := s.repo.CreateInvite(ctx, db.CreateInviteParams{
		Code:      code,
		GuildID:   guildID,
		ChannelID: channelID,
		CreatorID: callerID,
		MaxUses:   maxUses,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return db.Invite{}, fmt.Errorf("failed to create invite: %w", err)
	}

	return invite, nil
}

func (s *Service) CreateRole(ctx context.Context, callerID, guildID pgtype.UUID, name, color string) (db.Role, error) {
	if !s.authz.CanManageRoles(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return db.Role{}, ErrInsufficientPermissions
	}

	// Get current role count for position
	roles, err := s.repo.ListGuildRoles(ctx, guildID)
	if err != nil {
		return db.Role{}, fmt.Errorf("failed to list roles: %w", err)
	}

	role, err := s.repo.CreateRole(ctx, db.CreateRoleParams{
		GuildID:  guildID,
		Name:     name,
		Color:    color,
		Position: int32(len(roles)),
	})
	if err != nil {
		return db.Role{}, fmt.Errorf("failed to create role: %w", err)
	}

	// Register the role in the authz graph so permission grants can be
	// attached to it. Best-effort: a failure here leaves the Postgres row
	// in place and the caller can retry by re-saving permissions.
	_ = s.authz.BindRoleToGuild(ctx, pgToStr(role.ID), pgToStr(guildID))

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_ROLE_CREATE, map[string]string{
		"name":    role.Name,
		"role_id": pgToStr(role.ID),
	})

	return role, nil
}

func (s *Service) UpdateRole(ctx context.Context, callerID, guildID pgtype.UUID, params db.UpdateRoleParams) (db.Role, error) {
	if !s.authz.CanManageRoles(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return db.Role{}, ErrInsufficientPermissions
	}

	role, err := s.repo.UpdateRole(ctx, params)
	if err != nil {
		return db.Role{}, fmt.Errorf("failed to update role: %w", err)
	}

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_ROLE_UPDATE, nil)

	return role, nil
}

func (s *Service) DeleteRole(ctx context.Context, callerID, guildID, roleID pgtype.UUID) error {
	if !s.authz.CanManageRoles(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return ErrInsufficientPermissions
	}

	_, roleErr := s.repo.GetRoleByID(ctx, roleID)
	if roleErr != nil {
		return ErrRoleNotFound
	}

	if err := s.repo.DeleteRole(ctx, roleID); err != nil {
		return err
	}

	// Best-effort cleanup. Leftover role tuples in OpenFGA don't cause
	// correctness issues (the Postgres side is the source of truth for
	// role existence) but they waste space.
	_ = s.authz.UnbindRoleFromGuild(ctx, pgToStr(roleID), pgToStr(guildID))

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_ROLE_DELETE, map[string]string{
		"role_id": pgToStr(roleID),
	})

	return nil
}

func (s *Service) AssignRole(ctx context.Context, callerID, guildID, targetID, roleID pgtype.UUID) error {
	if !s.authz.CanManageRoles(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return ErrInsufficientPermissions
	}

	// Verify target is a member
	if _, memErr := s.repo.GetGuildMember(ctx, db.GetGuildMemberParams{GuildID: guildID, UserID: targetID}); memErr != nil {
		return ErrMemberNotFound
	}

	// Verify role exists
	if _, roleErr := s.repo.GetRoleByID(ctx, roleID); roleErr != nil {
		return ErrRoleNotFound
	}

	if err := s.repo.AssignRole(ctx, db.AssignRoleParams{RoleID: roleID, UserID: targetID}); err != nil {
		return err
	}

	// Write the authz tuple so permissions granted to the role propagate
	// to the user on the next Check. Target user must already be a guild
	// member (verified above) — the role scopes them further.
	_ = s.authz.AddUserToRole(ctx, pgToStr(targetID), pgToStr(roleID))

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_ROLE_ASSIGN, map[string]string{
		"user_id": pgToStr(targetID),
		"role_id": pgToStr(roleID),
	})

	return nil
}

func (s *Service) RemoveRole(ctx context.Context, callerID, guildID, targetID, roleID pgtype.UUID) error {
	if !s.authz.CanManageRoles(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return ErrInsufficientPermissions
	}

	if err := s.repo.RemoveRole(ctx, db.RemoveRoleParams{
		RoleID: roleID,
		UserID: targetID,
	}); err != nil {
		return err
	}

	_ = s.authz.RemoveUserFromRole(ctx, pgToStr(targetID), pgToStr(roleID))

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_ROLE_REMOVE, map[string]string{
		"user_id": pgToStr(targetID),
		"role_id": pgToStr(roleID),
	})

	return nil
}

// GrantRolePermission writes an OpenFGA tuple so every member of the
// given role inherits the named permission in the guild. `permission`
// is one of the Discord-style keys in authz.PermissionRelation (e.g.
// "KICK_MEMBERS", "BAN_MEMBERS", "SEND_MESSAGES").
func (s *Service) GrantRolePermission(ctx context.Context, callerID, guildID, roleID pgtype.UUID, permission string) error {
	if !s.authz.CanManageRoles(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return ErrInsufficientPermissions
	}
	if _, err := s.repo.GetRoleByID(ctx, roleID); err != nil {
		return ErrRoleNotFound
	}
	relation, ok := authz.PermissionRelation[permission]
	if !ok {
		return ErrInvalidPermission
	}
	if err := s.authz.GrantRolePermission(ctx, pgToStr(roleID), pgToStr(guildID), relation); err != nil {
		return fmt.Errorf("failed to grant role permission: %w", err)
	}
	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_ROLE_UPDATE, map[string]string{
		"role_id": pgToStr(roleID),
	})
	return nil
}

// RevokeRolePermission removes a previously-granted permission from a role.
func (s *Service) RevokeRolePermission(ctx context.Context, callerID, guildID, roleID pgtype.UUID, permission string) error {
	if !s.authz.CanManageRoles(ctx, pgToStr(callerID), pgToStr(guildID)) {
		return ErrInsufficientPermissions
	}
	if _, err := s.repo.GetRoleByID(ctx, roleID); err != nil {
		return ErrRoleNotFound
	}
	relation, ok := authz.PermissionRelation[permission]
	if !ok {
		return ErrInvalidPermission
	}
	if err := s.authz.RevokeRolePermission(ctx, pgToStr(roleID), pgToStr(guildID), relation); err != nil {
		return fmt.Errorf("failed to revoke role permission: %w", err)
	}
	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_ROLE_UPDATE, map[string]string{
		"role_id": pgToStr(roleID),
	})
	return nil
}

func (s *Service) TransferOwnership(ctx context.Context, callerID, guildID, newOwnerID pgtype.UUID) (db.Guild, error) {
	// Only the current owner can transfer ownership
	guild, err := s.repo.GetGuildByID(ctx, guildID)
	if err != nil {
		return db.Guild{}, fmt.Errorf("%w: %v", ErrGuildNotFound, err)
	}
	if guild.OwnerID != callerID {
		return db.Guild{}, ErrNotGuildOwner
	}

	// New owner must be a member of the guild
	_, err = s.repo.GetGuildMember(ctx, db.GetGuildMemberParams{
		GuildID: guildID,
		UserID:  newOwnerID,
	})
	if err != nil {
		return db.Guild{}, ErrMemberNotFound
	}

	// Update DB
	updated, err := s.repo.TransferGuildOwnership(ctx, db.TransferGuildOwnershipParams{
		ID:      guildID,
		OwnerID: newOwnerID,
	})
	if err != nil {
		return db.Guild{}, fmt.Errorf("failed to transfer ownership: %w", err)
	}

	callerStr := pgToStr(callerID)
	newOwnerStr := pgToStr(newOwnerID)
	guildStr := pgToStr(guildID)

	// Update OpenFGA tuples: remove old owner, add new owner
	_ = s.authz.DeleteTuple(ctx, authz.UserKey(callerStr), "owner", authz.GuildKey(guildStr))
	_ = s.authz.AddGuildOwner(ctx, newOwnerStr, guildStr)

	s.publishGuildEvent(ctx, guildID, streamv1.GuildEventType_GUILD_EVENT_UPDATE, map[string]string{
		"transferred_to": newOwnerStr,
	})

	return updated, nil
}

// generateInviteCode generates a random 8-character hex invite code.
func generateInviteCode() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
