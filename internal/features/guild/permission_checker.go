package guild

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/ananddub/ndiscord_backend/internal/shared/permissions"
)

// PermissionChecker implements gateway.PermissionChecker using the guild service.
type PermissionChecker struct {
	svc *Service
}

func NewPermissionChecker(svc *Service) *PermissionChecker {
	return &PermissionChecker{svc: svc}
}

func (p *PermissionChecker) HasPermission(ctx context.Context, userID, guildID string, perm int64) bool {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false
	}
	gid, err := uuid.Parse(guildID)
	if err != nil {
		return false
	}

	pgUser := pgtype.UUID{Bytes: uid, Valid: true}
	pgGuild := pgtype.UUID{Bytes: gid, Valid: true}

	computed, err := p.svc.ComputePermissions(ctx, pgUser, pgGuild)
	if err != nil {
		return false
	}

	return permissions.Has(computed, perm)
}

func (p *PermissionChecker) CanViewChannel(ctx context.Context, userID, guildID, channelID string) bool {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return false
	}
	gid, err := uuid.Parse(guildID)
	if err != nil {
		return false
	}

	pgUser := pgtype.UUID{Bytes: uid, Valid: true}
	pgGuild := pgtype.UUID{Bytes: gid, Valid: true}

	computed, err := p.svc.ComputePermissions(ctx, pgUser, pgGuild)
	if err != nil {
		return false
	}

	return permissions.Has(computed, permissions.ViewChannels)
}
