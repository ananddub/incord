package channel

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ananddub/ndiscord_backend/gen/db"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

type Service struct {
	repo  *Repository
	authz *authz.Client
	lpb   *realtime.LPubSub
}

func NewService(repo *Repository, nats *realtime.LPubSub, authzClient ...*authz.Client) *Service {
	s := &Service{repo: repo, lpb: nats}
	if len(authzClient) > 0 {
		s.authz = authzClient[0]
	}
	return s
}

// SetChannelOverride upserts a per-channel allow/deny rule and
// broadcasts a CHANNEL_UPDATE so connected members refresh their cached
// channel permission set. MANAGE_CHANNELS on the parent guild is the
// gate — channel-level perms are a guild-admin task in Discord.
func (s *Service) SetChannelOverride(ctx context.Context, userID, channelID, targetType, targetID, permission, effect string) error {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}
	ch, err := s.repo.GetChannelByID(ctx, chUUID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}
	if !ch.GuildID.Valid {
		return fmt.Errorf("channel overrides are only valid on guild channels")
	}
	guildID := uuidToString(ch.GuildID)
	if !s.authz.CanManageChannels(ctx, userID, guildID) {
		return ErrInsufficientPermissions
	}
	relation, ok := authz.PermissionRelation[permission]
	if !ok {
		return fmt.Errorf("unknown permission %q", permission)
	}
	if err := s.authz.SetChannelOverride(ctx, channelID, targetType, targetID, relation, effect); err != nil {
		return err
	}
	s.broadcastChannelChange(guildID, channelID, ch.Name, ch.Type)
	return nil
}

// DeleteChannelOverride removes a previously-set rule and broadcasts
// the same CHANNEL_UPDATE.
func (s *Service) DeleteChannelOverride(ctx context.Context, userID, channelID, targetType, targetID, permission string) error {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}
	ch, err := s.repo.GetChannelByID(ctx, chUUID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}
	if !ch.GuildID.Valid {
		return fmt.Errorf("channel overrides are only valid on guild channels")
	}
	guildID := uuidToString(ch.GuildID)
	if !s.authz.CanManageChannels(ctx, userID, guildID) {
		return ErrInsufficientPermissions
	}
	relation, ok := authz.PermissionRelation[permission]
	if !ok {
		return fmt.Errorf("unknown permission %q", permission)
	}
	if err := s.authz.DeleteChannelOverride(ctx, channelID, targetType, targetID, relation); err != nil {
		return err
	}
	s.broadcastChannelChange(guildID, channelID, ch.Name, ch.Type)
	return nil
}

// ListChannelOverrides returns every override currently set on a channel.
// Read permission — any guild member can inspect channel settings.
func (s *Service) ListChannelOverrides(ctx context.Context, userID, channelID string) ([]authz.ChannelOverride, error) {
	if !s.authz.CanViewChannel(ctx, userID, channelID) {
		return nil, ErrInsufficientPermissions
	}
	return s.authz.ListChannelOverrides(ctx, channelID)
}

// broadcastChannelChange emits a GUILD_EVENT_CHANNEL_UPDATE so every
// member subscribing to guild.<G>.events re-fetches the channel's
// effective permission view. Reuses the existing channel-update event
// type since override changes *are* channel changes from the client's
// perspective.
func (s *Service) broadcastChannelChange(guildID, channelID, name string, channelType int32) {
	if s.lpb == nil {
		return
	}
	evt := streamv1.GuildEventType_GUILD_EVENT_CHANNEL_UPDATE
	realtime.Publish(s.lpb, realtime.GuildEvents(guildID), streamv1.GuildEvent{
		Event:     evt,
		Action:    evt,
		GuildId:   guildID,
		ChannelId: channelID,
		Name:      name,
		Type:      int64(channelType),
	})
}

func (s *Service) CreateChannel(ctx context.Context, userID string, guildID, name string, channelType int32, topic, parentID string) (db.Channel, error) {
	if name == "" {
		return db.Channel{}, ErrNameRequired
	}

	guildUUID, err := parseUUID(guildID)
	if err != nil {
		return db.Channel{}, ErrGuildIDRequired
	}

	// Check guild membership
	userUUID, err := parseUUID(userID)
	if err != nil {
		return db.Channel{}, ErrInvalidUUID
	}

	_, err = s.repo.GetGuildMember(ctx, db.GetGuildMemberParams{
		GuildID: guildUUID,
		UserID:  userUUID,
	})
	if err != nil {
		return db.Channel{}, ErrNotGuildMember
	}

	if !s.authz.CanManageChannels(ctx, userID, guildID) {
		return db.Channel{}, ErrInsufficientPermissions
	}

	params := db.CreateChannelParams{
		GuildID: guildUUID,
		Name:    name,
		Type:    channelType,
		Topic:   topic,
	}

	if parentID != "" {
		pid, err := parseUUID(parentID)
		if err != nil {
			return db.Channel{}, fmt.Errorf("invalid parent_id: %w", ErrInvalidUUID)
		}
		params.ParentID = pid
	}

	ch, err := s.repo.CreateChannel(ctx, params)
	if err != nil {
		return db.Channel{}, fmt.Errorf("failed to create channel: %w", err)
	}

	_ = realtime.Publish(s.lpb, realtime.GuildEvents(guildID),
		streamv1.GuildEvent{
			Event:     streamv1.GuildEventType_GUILD_EVENT_CHANNEL_CREATE,
			GuildId:   guildID,
			ChannelId: uuidToString(ch.ID),
			Name:      ch.Name,
			Type:      int64(ch.Type),
			Topic:     ch.Topic,
			Position:  ch.Position,
			ParentId:  uuidToString(ch.ParentID),
		})

	return ch, nil
}

func (s *Service) GetChannel(ctx context.Context, channelID string) (db.Channel, error) {
	id, err := parseUUID(channelID)
	if err != nil {
		return db.Channel{}, ErrInvalidUUID
	}

	ch, err := s.repo.GetChannelByID(ctx, id)
	if err != nil {
		return db.Channel{}, fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}
	return ch, nil
}

func (s *Service) UpdateChannel(ctx context.Context, userID, channelID string, name, topic *string, position *int32, parentID *string) (db.Channel, error) {
	id, err := parseUUID(channelID)
	if err != nil {
		return db.Channel{}, ErrInvalidUUID
	}

	existing, err := s.repo.GetChannelByID(ctx, id)
	if err != nil {
		return db.Channel{}, fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}
	if existing.GuildID.Valid {
		if !s.authz.CanManageChannels(ctx, userID, uuidToString(existing.GuildID)) {
			return db.Channel{}, ErrInsufficientPermissions
		}
	}

	params := db.UpdateChannelParams{
		ID:    id,
		Name:  name,
		Topic: topic,
	}

	if position != nil {
		params.Position = position
	}

	if parentID != nil {
		pid, err := parseUUID(*parentID)
		if err != nil {
			return db.Channel{}, fmt.Errorf("invalid parent_id: %w", ErrInvalidUUID)
		}
		params.ParentID = pid
	}

	ch, err := s.repo.UpdateChannel(ctx, params)
	if err != nil {
		return db.Channel{}, fmt.Errorf("failed to update channel: %w", err)
	}

	if ch.GuildID.Valid {
		gid := uuidToString(ch.GuildID)
		_ = realtime.Publish(s.lpb, realtime.GuildEvents(gid), streamv1.GuildEvent{
			Event:     streamv1.GuildEventType_GUILD_EVENT_CHANNEL_UPDATE,
			GuildId:   gid,
			ChannelId: uuidToString(ch.ID),
			Name:      ch.Name,
			Type:      int64(ch.Type),
			Topic:     ch.Topic,
			Position:  ch.Position,
			ParentId:  uuidToString(ch.ParentID),
		})
	}

	return ch, nil
}

func (s *Service) DeleteChannel(ctx context.Context, userID, channelID string) error {
	id, err := parseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}

	ch, err := s.repo.GetChannelByID(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}

	if ch.GuildID.Valid {
		if !s.authz.CanManageChannels(ctx, userID, uuidToString(ch.GuildID)) {
			return ErrInsufficientPermissions
		}
	}

	var dmMembers []string
	isDM := !ch.GuildID.Valid && (ch.Type == 5 || ch.Type == 6)
	if isDM {
		dmMembers = s.dmChannelMemberIDs(ctx, id)
	}

	if err := s.repo.DeleteChannel(ctx, id); err != nil {
		return fmt.Errorf("failed to delete channel: %w", err)
	}

	if ch.GuildID.Valid {
		gid := uuidToString(ch.GuildID)
		_ = realtime.Publish(s.lpb, realtime.GuildEvents(gid), streamv1.GuildEvent{
			Event:     streamv1.GuildEventType_GUILD_EVENT_CHANNEL_DELETE,
			GuildId:   gid,
			ChannelId: channelID,
		})
	}

	if isDM {
		s.publishDMChannelEvent(ctx, streamv1.ChannelLifecycleType_CHANNEL_LIFECYCLE_DELETE, ch, dmMembers)
	}

	return nil
}

func (s *Service) ListGuildChannels(ctx context.Context, guildID string) ([]db.Channel, error) {
	id, err := parseUUID(guildID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	channels, err := s.repo.ListGuildChannels(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list guild channels: %w", err)
	}
	return channels, nil
}

func (s *Service) CreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (db.Channel, error) {
	if len(recipientIDs) == 0 {
		return db.Channel{}, ErrRecipientRequired
	}

	userUUID, err := parseUUID(userID)
	if err != nil {
		return db.Channel{}, ErrInvalidUUID
	}

	// For 1:1 DM, check if channel already exists
	if len(recipientIDs) == 1 {
		recipientUUID, err := parseUUID(recipientIDs[0])
		if err != nil {
			return db.Channel{}, ErrInvalidUUID
		}

		existing, err := s.repo.GetDMChannelBetweenUsers(ctx, db.GetDMChannelBetweenUsersParams{
			UserID:   userUUID,
			UserID_2: recipientUUID,
		})
		if err == nil {
			return existing, nil
		}
	}

	// Determine channel type: DM (5) for 1 recipient, GROUP_DM (6) for multiple
	channelType := int32(5)
	if len(recipientIDs) > 1 {
		channelType = int32(6)
	}

	ch, err := s.repo.CreateDMChannel(ctx, db.CreateDMChannelParams{
		Name: "DM",
		Type: channelType,
	})
	if err != nil {
		return db.Channel{}, fmt.Errorf("failed to create DM channel: %w", err)
	}

	if err := s.repo.AddDMChannelMember(ctx, db.AddDMChannelMemberParams{
		ChannelID: ch.ID,
		UserID:    userUUID,
	}); err != nil {
		return db.Channel{}, fmt.Errorf("failed to add creator to DM channel: %w", err)
	}

	for _, rid := range recipientIDs {
		rUUID, err := parseUUID(rid)
		if err != nil {
			return db.Channel{}, ErrInvalidUUID
		}
		if err := s.repo.AddDMChannelMember(ctx, db.AddDMChannelMemberParams{
			ChannelID: ch.ID,
			UserID:    rUUID,
		}); err != nil {
			return db.Channel{}, fmt.Errorf("failed to add recipient to DM channel: %w", err)
		}
	}

	s.publishDMChannelCreated(ctx, ch, append([]string{userID}, recipientIDs...))

	return ch, nil
}

func (s *Service) publishDMChannelCreated(ctx context.Context, ch db.Channel, memberIDs []string) {
	s.publishDMChannelEvent(ctx, streamv1.ChannelLifecycleType_CHANNEL_LIFECYCLE_CREATE, ch, memberIDs)
}

func (s *Service) publishDMChannelEvent(ctx context.Context, eventType streamv1.ChannelLifecycleType, ch db.Channel, memberIDs []string) {
	if s.lpb == nil {
		return
	}
	var members []*streamv1.DmChannelMember
	if eventType != streamv1.ChannelLifecycleType_CHANNEL_LIFECYCLE_DELETE {
		profiles, err := s.repo.GetDMChannelMemberProfiles(ctx, ch.ID)
		if err != nil {
			return
		}
		members = make([]*streamv1.DmChannelMember, len(profiles))
		for i, p := range profiles {
			members[i] = &streamv1.DmChannelMember{
				Id:          uuidToString(p.ID),
				Username:    p.Username,
				DisplayName: p.DisplayName,
				AvatarUrl:   p.AvatarUrl,
				Status:      p.Status,
			}
		}
	}
	payload := &streamv1.DmChannelEvent{
		Type:        eventType,
		Id:          uuidToString(ch.ID),
		Name:        ch.Name,
		ChannelType: int32(ch.Type),
		Members:     members,
	}
	for _, mid := range memberIDs {
		_ = realtime.Publish(s.lpb, realtime.DmChannels(mid), payload)
	}
}

func (s *Service) dmChannelMemberIDs(ctx context.Context, channelID pgtype.UUID) []string {
	rows, err := s.repo.GetDMChannelMembers(ctx, channelID)
	if err != nil {
		return nil
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = uuidToString(r)
	}
	return out
}

func (s *Service) ListDMChannels(ctx context.Context, userID string) ([]db.Channel, error) {
	id, err := parseUUID(userID)
	if err != nil {
		return nil, ErrInvalidUUID
	}

	channels, err := s.repo.ListDMChannels(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list DM channels: %w", err)
	}
	return channels, nil
}

func (s *Service) ListDMChannelMembers(ctx context.Context, callerID, channelID string) ([]db.GetDMChannelMemberProfilesRow, error) {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	callerUUID, err := parseUUID(callerID)
	if err != nil {
		return nil, ErrInvalidUUID
	}

	isMember, err := s.repo.IsDMChannelMember(ctx, db.IsDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    callerUUID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if !isMember {
		return nil, ErrNotDMChannelMember
	}

	return s.repo.GetDMChannelMemberProfiles(ctx, chUUID)
}

type DMChannelWithMembers struct {
	Channel db.Channel
	Members []db.GetDMChannelMemberProfilesRow
}

func (s *Service) ListDMChannelsWithMembers(ctx context.Context, userID string) ([]DMChannelWithMembers, error) {
	id, err := parseUUID(userID)
	if err != nil {
		return nil, ErrInvalidUUID
	}

	channels, err := s.repo.ListDMChannels(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to list DM channels: %w", err)
	}

	out := make([]DMChannelWithMembers, 0, len(channels))
	for _, ch := range channels {
		members, err := s.repo.GetDMChannelMemberProfiles(ctx, ch.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to load members for channel: %w", err)
		}
		out = append(out, DMChannelWithMembers{Channel: ch, Members: members})
	}
	return out, nil
}

func (s *Service) AddDMGroupMember(ctx context.Context, callerID, channelID, userID string) error {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}

	ch, err := s.repo.GetChannelByID(ctx, chUUID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}
	if ch.Type != 6 {
		return ErrNotGroupDM
	}

	callerUUID, err := parseUUID(callerID)
	if err != nil {
		return ErrInvalidUUID
	}
	isMember, err := s.repo.IsDMChannelMember(ctx, db.IsDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    callerUUID,
	})
	if err != nil || !isMember {
		return ErrNotDMChannelMember
	}

	// Check target is not already a member
	targetUUID, err := parseUUID(userID)
	if err != nil {
		return ErrInvalidUUID
	}
	alreadyMember, err := s.repo.IsDMChannelMember(ctx, db.IsDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    targetUUID,
	})
	if err == nil && alreadyMember {
		return ErrAlreadyDMChannelMember
	}

	if err := s.repo.AddDMChannelMember(ctx, db.AddDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    targetUUID,
	}); err != nil {
		return fmt.Errorf("failed to add member to group DM: %w", err)
	}

	s.publishDMChannelEvent(ctx, streamv1.ChannelLifecycleType_CHANNEL_LIFECYCLE_UPDATE, ch, s.dmChannelMemberIDs(ctx, chUUID))

	return nil
}

func (s *Service) RemoveDMGroupMember(ctx context.Context, callerID, channelID, userID string) error {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}

	// Verify channel exists and is a group DM (type=6)
	ch, err := s.repo.GetChannelByID(ctx, chUUID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}
	if ch.Type != 6 {
		return ErrNotGroupDM
	}

	callerUUID, err := parseUUID(callerID)
	if err != nil {
		return ErrInvalidUUID
	}
	isMember, err := s.repo.IsDMChannelMember(ctx, db.IsDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    callerUUID,
	})
	if err != nil || !isMember {
		return ErrNotDMChannelMember
	}

	targetUUID, err := parseUUID(userID)
	if err != nil {
		return ErrInvalidUUID
	}
	targetIsMember, err := s.repo.IsDMChannelMember(ctx, db.IsDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    targetUUID,
	})
	if err != nil || !targetIsMember {
		return ErrNotDMChannelMember
	}

	beforeMembers := s.dmChannelMemberIDs(ctx, chUUID)

	if err := s.repo.RemoveDMChannelMember(ctx, db.RemoveDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    targetUUID,
	}); err != nil {
		return fmt.Errorf("failed to remove member from group DM: %w", err)
	}

	s.publishDMChannelEvent(ctx, streamv1.ChannelLifecycleType_CHANNEL_LIFECYCLE_UPDATE, ch, beforeMembers)

	return nil
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	return u, nil
}

func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

type DMResolver struct{ svc *Service }

func NewDMResolver(svc *Service) *DMResolver { return &DMResolver{svc: svc} }

func (r *DMResolver) GetOrCreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (string, error) {
	ch, err := r.svc.CreateDMChannel(ctx, userID, recipientIDs)
	if err != nil {
		return "", err
	}
	return uuidToString(ch.ID), nil
}

func (r *DMResolver) IsDMMember(ctx context.Context, channelID, userID string) (bool, error) {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return false, ErrInvalidUUID
	}
	userUUID, err := parseUUID(userID)
	if err != nil {
		return false, ErrInvalidUUID
	}
	return r.svc.repo.IsDMChannelMember(ctx, db.IsDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    userUUID,
	})
}

func (r *DMResolver) DMMembers(ctx context.Context, channelID string) ([]string, error) {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	return r.svc.dmChannelMemberIDs(ctx, chUUID), nil
}

type GuildResolver struct{ svc *Service }

func NewGuildResolver(svc *Service) *GuildResolver { return &GuildResolver{svc: svc} }

func (r *GuildResolver) GetChannelGuildID(ctx context.Context, channelID string) string {
	ch, err := r.svc.GetChannel(ctx, channelID)
	if err != nil || !ch.GuildID.Valid {
		return ""
	}
	return uuidToString(ch.GuildID)
}

type DMChannelMembersResolver struct{ repo *Repository }

func NewDMChannelMembersResolver(repo *Repository) *DMChannelMembersResolver {
	return &DMChannelMembersResolver{repo: repo}
}

func (r *DMChannelMembersResolver) GetDMChannelMemberIDs(ctx context.Context, channelID string) ([]string, error) {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return nil, err
	}
	members, err := r.repo.GetDMChannelMembers(ctx, chUUID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(members))
	for _, m := range members {
		ids = append(ids, uuidToString(m))
	}
	return ids, nil
}

func (r *DMChannelMembersResolver) GetUserDMChannelIDs(ctx context.Context, userID string) ([]string, error) {
	uid, err := parseUUID(userID)
	if err != nil {
		return nil, err
	}
	channels, err := r.repo.ListDMChannels(ctx, uid)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(channels))
	for _, ch := range channels {
		ids = append(ids, uuidToString(ch.ID))
	}
	return ids, nil
}
