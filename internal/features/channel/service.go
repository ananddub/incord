package channel

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

type Service struct {
	repo  *Repository
	authz *authz.Client
	nats  *realtime.Hub
}

func NewService(repo *Repository, nats *realtime.Hub, authzClient ...*authz.Client) *Service {
	s := &Service{repo: repo, nats: nats}
	if len(authzClient) > 0 {
		s.authz = authzClient[0]
	}
	return s
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

	_ = s.authz.SetChannelGuild(ctx, ch.ID.String(), guildID)

	_ = s.nats.Publish(realtime.GuildEvents(guildID), map[string]any{
		"event":      "CHANNEL_CREATE",
		"guild_id":   guildID,
		"channel_id": ch.ID.String(),
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
		_ = s.nats.Publish(realtime.GuildEvents(gid), map[string]any{
			"event":      "CHANNEL_UPDATE",
			"guild_id":   gid,
			"channel_id": uuidToString(ch.ID),
		})
	}

	return ch, nil
}

func (s *Service) DeleteChannel(ctx context.Context, userID, channelID string) error {
	id, err := parseUUID(channelID)
	if err != nil {
		return ErrInvalidUUID
	}

	// Fetch channel first so we can publish the event with guild_id
	ch, err := s.repo.GetChannelByID(ctx, id)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}

	if ch.GuildID.Valid {
		if !s.authz.CanManageChannels(ctx, userID, uuidToString(ch.GuildID)) {
			return ErrInsufficientPermissions
		}
	}

	// Capture DM member IDs BEFORE delete so all of them get notified.
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
		_ = s.nats.Publish(realtime.GuildEvents(gid), map[string]any{
			"event":      "CHANNEL_DELETE",
			"guild_id":   gid,
			"channel_id": channelID,
		})
	}

	if isDM {
		s.publishDMChannelEvent(ctx, "delete", ch, dmMembers)
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

	// Add the creator as a member
	if err := s.repo.AddDMChannelMember(ctx, db.AddDMChannelMemberParams{
		ChannelID: ch.ID,
		UserID:    userUUID,
	}); err != nil {
		return db.Channel{}, fmt.Errorf("failed to add creator to DM channel: %w", err)
	}

	// Add each recipient
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

	// Notify every member so their StreamDmChannels subscribers get the new
	// channel alongside the full member profile list in one push.
	s.publishDMChannelCreated(ctx, ch, append([]string{userID}, recipientIDs...))

	return ch, nil
}

// publishDMChannelCreated emits a "create" event on each member's
// DmChannels subject carrying the channel's full member profiles so clients
// can render the new DM without a follow-up ListDMChannelMembers call.
func (s *Service) publishDMChannelCreated(ctx context.Context, ch db.Channel, memberIDs []string) {
	s.publishDMChannelEvent(ctx, "create", ch, memberIDs)
}

// publishDMChannelEvent emits a lifecycle event ("create", "update", "delete")
// on every provided member's DmChannels subject. The payload carries the
// current member profile snapshot so each recipient can update its local
// channel list without refetching. For "delete", call with the pre-delete
// member list so all of them get notified.
func (s *Service) publishDMChannelEvent(ctx context.Context, eventType string, ch db.Channel, memberIDs []string) {
	if s.nats == nil {
		return
	}
	var members []map[string]any
	if eventType != "delete" {
		profiles, err := s.repo.GetDMChannelMemberProfiles(ctx, ch.ID)
		if err != nil {
			return
		}
		members = make([]map[string]any, len(profiles))
		for i, p := range profiles {
			members[i] = map[string]any{
				"id":           uuidToString(p.ID),
				"username":     p.Username,
				"display_name": p.DisplayName,
				"avatar_url":   p.AvatarUrl,
				"status":       p.Status,
			}
		}
	}
	payload := map[string]any{
		"type":         eventType,
		"id":           uuidToString(ch.ID),
		"name":         ch.Name,
		"channel_type": ch.Type,
		"members":      members,
	}
	for _, mid := range memberIDs {
		_ = s.nats.Publish(realtime.DmChannels(mid), payload)
	}
}

// dmChannelMemberIDs returns the current member user_ids of a DM channel as
// strings. Used to fan out lifecycle events to everyone in the channel.
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

// ListDMChannelMembers returns lightweight profiles for everyone in a DM
// channel. Caller must themselves be a member.
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

// DMChannelWithMembers bundles a DM channel with embedded member profiles.
type DMChannelWithMembers struct {
	Channel db.Channel
	Members []db.GetDMChannelMemberProfilesRow
}

// ListDMChannelsWithMembers returns every DM channel the caller is in along
// with embedded member profiles for each channel in one call.
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

	// Verify channel exists and is a group DM (type=6)
	ch, err := s.repo.GetChannelByID(ctx, chUUID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrChannelNotFound, err)
	}
	if ch.Type != 6 {
		return ErrNotGroupDM
	}

	// Caller must be a member
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

	// Notify every current member (including the newly added one) with the
	// refreshed member profile list so each client updates its view.
	s.publishDMChannelEvent(ctx, "update", ch, s.dmChannelMemberIDs(ctx, chUUID))

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

	// Caller must be a member
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

	// Verify target is a member
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

	// Capture the member list BEFORE the remove so the target user also gets
	// notified that they were removed.
	beforeMembers := s.dmChannelMemberIDs(ctx, chUUID)

	if err := s.repo.RemoveDMChannelMember(ctx, db.RemoveDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    targetUUID,
	}); err != nil {
		return fmt.Errorf("failed to remove member from group DM: %w", err)
	}

	s.publishDMChannelEvent(ctx, "update", ch, beforeMembers)

	return nil
}

// parseUUID converts a string UUID to a pgtype.UUID.
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, fmt.Errorf("invalid UUID %q: %w", s, err)
	}
	return u, nil
}

// uuidToString converts a pgtype.UUID to its string representation.
func uuidToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	b := u.Bytes
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ── Resolvers (used by message service) ──

// DMResolver implements message.DMChannelResolver.
type DMResolver struct{ svc *Service }

func NewDMResolver(svc *Service) *DMResolver { return &DMResolver{svc: svc} }

func (r *DMResolver) GetOrCreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (string, error) {
	ch, err := r.svc.CreateDMChannel(ctx, userID, recipientIDs)
	if err != nil {
		return "", err
	}
	return uuidToString(ch.ID), nil
}

// IsDMMember implements voice.DMMembershipResolver.
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

// DMMembers implements voice.DMMembershipResolver.
func (r *DMResolver) DMMembers(ctx context.Context, channelID string) ([]string, error) {
	chUUID, err := parseUUID(channelID)
	if err != nil {
		return nil, ErrInvalidUUID
	}
	return r.svc.dmChannelMemberIDs(ctx, chUUID), nil
}

// GuildResolver implements message.ChannelGuildResolver.
type GuildResolver struct{ svc *Service }

func NewGuildResolver(svc *Service) *GuildResolver { return &GuildResolver{svc: svc} }

func (r *GuildResolver) GetChannelGuildID(ctx context.Context, channelID string) string {
	ch, err := r.svc.GetChannel(ctx, channelID)
	if err != nil || !ch.GuildID.Valid {
		return ""
	}
	return uuidToString(ch.GuildID)
}

// DMChannelMembersResolver implements message.DMChannelLister + dispatcher.DMChannelMembers.
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
