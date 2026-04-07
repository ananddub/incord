package auth

import (
	"context"
	"fmt"
	"time"

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

func (r *Repository) CreateUser(ctx context.Context, username, email, passwordHash string) (db.User, error) {
	return r.queries.CreateUser(ctx, db.CreateUserParams{
		Username:     username,
		Email:        email,
		PasswordHash: passwordHash,
	})
}

func (r *Repository) GetUserByEmail(ctx context.Context, email string) (db.User, error) {
	return r.queries.GetUserByEmail(ctx, email)
}

func (r *Repository) StoreRefreshToken(ctx context.Context, userID, token string, ttl time.Duration) error {
	key := fmt.Sprintf("refresh:%s", token)
	return r.redis.Set(ctx, key, userID, ttl).Err()
}

func (r *Repository) GetRefreshToken(ctx context.Context, token string) (string, error) {
	key := fmt.Sprintf("refresh:%s", token)
	return r.redis.Get(ctx, key).Result()
}

func (r *Repository) DeleteRefreshToken(ctx context.Context, token string) error {
	key := fmt.Sprintf("refresh:%s", token)
	return r.redis.Del(ctx, key).Err()
}

// OTP storage in Redis (5 minute TTL)
func (r *Repository) StoreOTP(ctx context.Context, email, otp string) error {
	key := fmt.Sprintf("otp:%s", email)
	return r.redis.Set(ctx, key, otp, 5*time.Minute).Err()
}

func (r *Repository) GetOTP(ctx context.Context, email string) (string, error) {
	key := fmt.Sprintf("otp:%s", email)
	return r.redis.Get(ctx, key).Result()
}

func (r *Repository) DeleteOTP(ctx context.Context, email string) error {
	key := fmt.Sprintf("otp:%s", email)
	return r.redis.Del(ctx, key).Err()
}

func (r *Repository) VerifyUser(ctx context.Context, userID pgtype.UUID) (db.User, error) {
	return r.queries.VerifyUser(ctx, userID)
}
