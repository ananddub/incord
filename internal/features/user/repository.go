package user

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

func (r *Repository) GetUserByID(ctx context.Context, id pgtype.UUID) (db.User, error) {
	return r.queries.GetUserByID(ctx, id)
}

func (r *Repository) GetUserByUsername(ctx context.Context, username string) (db.User, error) {
	return r.queries.GetUserByUsername(ctx, username)
}

func (r *Repository) UpdateUser(ctx context.Context, params db.UpdateUserParams) (db.User, error) {
	return r.queries.UpdateUser(ctx, params)
}

func (r *Repository) DeleteUser(ctx context.Context, id pgtype.UUID) error {
	return r.queries.DeleteUser(ctx, id)
}

func (r *Repository) SearchUsers(ctx context.Context, params db.SearchUsersParams) ([]db.User, error) {
	return r.queries.SearchUsers(ctx, params)
}

func (r *Repository) CountSearchUsers(ctx context.Context, query *string) (int64, error) {
	return r.queries.CountSearchUsers(ctx, query)
}

func (r *Repository) CreateFriendship(ctx context.Context, params db.CreateFriendshipParams) (db.Friendship, error) {
	return r.queries.CreateFriendship(ctx, params)
}

func (r *Repository) UpdateFriendshipStatus(ctx context.Context, params db.UpdateFriendshipStatusParams) (db.Friendship, error) {
	return r.queries.UpdateFriendshipStatus(ctx, params)
}

func (r *Repository) DeleteFriendship(ctx context.Context, params db.DeleteFriendshipParams) error {
	return r.queries.DeleteFriendship(ctx, params)
}

func (r *Repository) GetFriendship(ctx context.Context, params db.GetFriendshipParams) (db.Friendship, error) {
	return r.queries.GetFriendship(ctx, params)
}

func (r *Repository) ListFriends(ctx context.Context, userID pgtype.UUID) ([]db.User, error) {
	return r.queries.ListFriends(ctx, userID)
}

func (r *Repository) ListPendingIncoming(ctx context.Context, friendID pgtype.UUID) ([]db.Friendship, error) {
	return r.queries.ListPendingIncoming(ctx, friendID)
}

func (r *Repository) ListPendingOutgoing(ctx context.Context, userID pgtype.UUID) ([]db.Friendship, error) {
	return r.queries.ListPendingOutgoing(ctx, userID)
}

func (r *Repository) ListBlocked(ctx context.Context, userID pgtype.UUID) ([]db.User, error) {
	return r.queries.ListBlocked(ctx, userID)
}
