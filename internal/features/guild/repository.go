package guild

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/ananddub/ndiscord_backend/gen/db"
)

type Repository struct {
	queries *db.Queries
	redis   *redis.Client
}

func NewRepository(pool *pgxpool.Pool, redis *redis.Client) *Repository {
	return &Repository{
		queries: db.New(pool),
		redis:   redis,
	}
}

// Guild CRUD

func (r *Repository) CreateGuild(ctx context.Context, params db.CreateGuildParams) (db.Guild, error) {
	return r.queries.CreateGuild(ctx, params)
}

func (r *Repository) GetGuildByID(ctx context.Context, id pgtype.UUID) (db.Guild, error) {
	return r.queries.GetGuildByID(ctx, id)
}

func (r *Repository) UpdateGuild(ctx context.Context, params db.UpdateGuildParams) (db.Guild, error) {
	return r.queries.UpdateGuild(ctx, params)
}

func (r *Repository) DeleteGuild(ctx context.Context, id pgtype.UUID) error {
	return r.queries.DeleteGuild(ctx, id)
}

func (r *Repository) ListUserGuilds(ctx context.Context, userID pgtype.UUID) ([]db.Guild, error) {
	return r.queries.ListUserGuilds(ctx, userID)
}

// Members

func (r *Repository) AddGuildMember(ctx context.Context, params db.AddGuildMemberParams) (db.GuildMember, error) {
	return r.queries.AddGuildMember(ctx, params)
}

func (r *Repository) RemoveGuildMember(ctx context.Context, params db.RemoveGuildMemberParams) error {
	return r.queries.RemoveGuildMember(ctx, params)
}

func (r *Repository) GetGuildMember(ctx context.Context, params db.GetGuildMemberParams) (db.GuildMember, error) {
	return r.queries.GetGuildMember(ctx, params)
}

func (r *Repository) ListGuildMembers(ctx context.Context, params db.ListGuildMembersParams) ([]db.GuildMember, error) {
	return r.queries.ListGuildMembers(ctx, params)
}

func (r *Repository) CountGuildMembers(ctx context.Context, guildID pgtype.UUID) (int64, error) {
	return r.queries.CountGuildMembers(ctx, guildID)
}

// Bans

func (r *Repository) CreateBan(ctx context.Context, params db.CreateBanParams) (db.Ban, error) {
	return r.queries.CreateBan(ctx, params)
}

func (r *Repository) DeleteBan(ctx context.Context, params db.DeleteBanParams) error {
	return r.queries.DeleteBan(ctx, params)
}

func (r *Repository) GetBan(ctx context.Context, params db.GetBanParams) (db.Ban, error) {
	return r.queries.GetBan(ctx, params)
}

// Invites

func (r *Repository) CreateInvite(ctx context.Context, params db.CreateInviteParams) (db.Invite, error) {
	return r.queries.CreateInvite(ctx, params)
}

func (r *Repository) GetInvite(ctx context.Context, code string) (db.Invite, error) {
	return r.queries.GetInvite(ctx, code)
}

func (r *Repository) IncrementInviteUses(ctx context.Context, code string) error {
	return r.queries.IncrementInviteUses(ctx, code)
}

func (r *Repository) DeleteInvite(ctx context.Context, code string) error {
	return r.queries.DeleteInvite(ctx, code)
}

// Roles

func (r *Repository) CreateRole(ctx context.Context, params db.CreateRoleParams) (db.Role, error) {
	return r.queries.CreateRole(ctx, params)
}

func (r *Repository) GetRoleByID(ctx context.Context, id pgtype.UUID) (db.Role, error) {
	return r.queries.GetRoleByID(ctx, id)
}

func (r *Repository) UpdateRole(ctx context.Context, params db.UpdateRoleParams) (db.Role, error) {
	return r.queries.UpdateRole(ctx, params)
}

func (r *Repository) DeleteRole(ctx context.Context, id pgtype.UUID) error {
	return r.queries.DeleteRole(ctx, id)
}

func (r *Repository) ListGuildRoles(ctx context.Context, guildID pgtype.UUID) ([]db.Role, error) {
	return r.queries.ListGuildRoles(ctx, guildID)
}

func (r *Repository) AssignRole(ctx context.Context, params db.AssignRoleParams) error {
	return r.queries.AssignRole(ctx, params)
}

func (r *Repository) RemoveRole(ctx context.Context, params db.RemoveRoleParams) error {
	return r.queries.RemoveRole(ctx, params)
}

func (r *Repository) ListUserRoles(ctx context.Context, params db.ListUserRolesParams) ([]db.Role, error) {
	return r.queries.ListUserRoles(ctx, params)
}

func (r *Repository) TransferGuildOwnership(ctx context.Context, params db.TransferGuildOwnershipParams) (db.Guild, error) {
	return r.queries.TransferGuildOwnership(ctx, params)
}
