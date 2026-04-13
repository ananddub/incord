package channel

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ananddub/ndiscord_backend/gen/db"
)

type Repository struct {
	queries *db.Queries
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{
		queries: db.New(pool),
	}
}

func (r *Repository) CreateChannel(ctx context.Context, params db.CreateChannelParams) (db.Channel, error) {
	return r.queries.CreateChannel(ctx, params)
}

func (r *Repository) GetChannelByID(ctx context.Context, id pgtype.UUID) (db.Channel, error) {
	return r.queries.GetChannelByID(ctx, id)
}

func (r *Repository) UpdateChannel(ctx context.Context, params db.UpdateChannelParams) (db.Channel, error) {
	return r.queries.UpdateChannel(ctx, params)
}

func (r *Repository) DeleteChannel(ctx context.Context, id pgtype.UUID) error {
	return r.queries.DeleteChannel(ctx, id)
}

func (r *Repository) ListGuildChannels(ctx context.Context, guildID pgtype.UUID) ([]db.Channel, error) {
	return r.queries.ListGuildChannels(ctx, guildID)
}

func (r *Repository) CreateDMChannel(ctx context.Context, params db.CreateDMChannelParams) (db.Channel, error) {
	return r.queries.CreateDMChannel(ctx, params)
}

func (r *Repository) AddDMChannelMember(ctx context.Context, params db.AddDMChannelMemberParams) error {
	return r.queries.AddDMChannelMember(ctx, params)
}

func (r *Repository) ListDMChannels(ctx context.Context, userID pgtype.UUID) ([]db.Channel, error) {
	return r.queries.ListDMChannels(ctx, userID)
}

func (r *Repository) GetDMChannelBetweenUsers(ctx context.Context, params db.GetDMChannelBetweenUsersParams) (db.Channel, error) {
	return r.queries.GetDMChannelBetweenUsers(ctx, params)
}

func (r *Repository) GetGuildMember(ctx context.Context, params db.GetGuildMemberParams) (db.GuildMember, error) {
	return r.queries.GetGuildMember(ctx, params)
}

func (r *Repository) RemoveDMChannelMember(ctx context.Context, params db.RemoveDMChannelMemberParams) error {
	return r.queries.RemoveDMChannelMember(ctx, params)
}

func (r *Repository) IsDMChannelMember(ctx context.Context, params db.IsDMChannelMemberParams) (bool, error) {
	return r.queries.IsDMChannelMember(ctx, params)
}

func (r *Repository) GetDMChannelMembers(ctx context.Context, channelID pgtype.UUID) ([]pgtype.UUID, error) {
	return r.queries.GetDMChannelMembers(ctx, channelID)
}
