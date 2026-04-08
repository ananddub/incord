package channel

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/event"
)

type Service struct {
	repo     *Repository
	producer *event.Producer
	authz    *authz.Client
}

func NewService(repo *Repository, producer *event.Producer, authzClient ...*authz.Client) *Service {
	s := &Service{repo: repo, producer: producer}
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

	// Write authz tuple: channel belongs to guild
	_ = s.authz.SetChannelGuild(ctx, ch.ID.String(), guildID)

	_ = event.PublishEvent(ctx, s.producer, event.TopicChannelEvents, guildID, guildID, ch.ID.String(), "", map[string]any{
		"action":     "create",
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

	// Fetch channel to get guild_id for permission check
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
		_ = event.PublishEvent(ctx, s.producer, event.TopicChannelEvents, uuidToString(ch.GuildID), uuidToString(ch.GuildID), uuidToString(ch.ID), "", map[string]any{
			"action":     "update",
			"guild_id":   uuidToString(ch.GuildID),
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

	if err := s.repo.DeleteChannel(ctx, id); err != nil {
		return fmt.Errorf("failed to delete channel: %w", err)
	}

	if ch.GuildID.Valid {
		_ = event.PublishEvent(ctx, s.producer, event.TopicChannelEvents, uuidToString(ch.GuildID), uuidToString(ch.GuildID), channelID, "", map[string]any{
			"action":     "delete",
			"guild_id":   uuidToString(ch.GuildID),
			"channel_id": channelID,
		})
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

	return ch, nil
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

	if err := s.repo.RemoveDMChannelMember(ctx, db.RemoveDMChannelMemberParams{
		ChannelID: chUUID,
		UserID:    targetUUID,
	}); err != nil {
		return fmt.Errorf("failed to remove member from group DM: %w", err)
	}

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
