package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
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

func (r *Repository) CreateUser(ctx context.Context, username, displayName, email, passwordHash string) (db.User, error) {
	return r.queries.CreateUser(ctx, db.CreateUserParams{
		Username:     username,
		DisplayName:  displayName,
		Email:        email,
		PasswordHash: passwordHash,
	})
}

// CreateUserWithUsername takes a base username from the caller, assigns a
// random 4-digit discriminator, and persists the user with
// username="name#1234" and display_name=name. Retries on discriminator
// collisions (unique violation on users.username) up to maxHandleAttempts.
// Other unique violations (e.g. email) surface immediately.
func (r *Repository) CreateUserWithUsername(ctx context.Context, username, email, passwordHash string) (db.User, error) {
	const maxHandleAttempts = 10
	var lastErr error
	for i := 0; i < maxHandleAttempts; i++ {
		handle := fmt.Sprintf("%s#%s", username, randomDiscriminator())
		user, err := r.queries.CreateUser(ctx, db.CreateUserParams{
			Username:     handle,
			DisplayName:  username,
			Email:        email,
			PasswordHash: passwordHash,
		})
		if err == nil {
			return user, nil
		}
		lastErr = err
		if !isUniqueViolation(err) {
			return db.User{}, err
		}
	}
	return db.User{}, fmt.Errorf("failed to allocate unique handle after %d attempts: %w", maxHandleAttempts, lastErr)
}

// randomDiscriminator returns a 4-digit string (0001-9999). 0000 is reserved.
func randomDiscriminator() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(9999))
	return fmt.Sprintf("%04d", n.Int64()+1)
}

// isUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505).
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
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
