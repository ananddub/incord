package auth

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

func setupAuthService(t *testing.T) (*Service, *testutil.TestInfra) {
	t.Helper()
	infra := testutil.SetupTestInfra(t)

	repo := NewRepository(infra.Pool, infra.Redis)
	cfg := config.JWTConfig{
		Secret:          "test-secret",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	}
	svc := NewService(repo, cfg)
	return svc, infra
}

// registerAndVerify is a helper that registers a user and verifies via OTP,
// returning (userID, tokens). The OTP is fetched directly from Redis.
func registerAndVerify(t *testing.T, svc *Service, infra *testutil.TestInfra, username, email, password string) (string, *TokenPair) {
	t.Helper()
	ctx := context.Background()

	userID, err := svc.Register(ctx, username, email, password)
	require.NoError(t, err)
	require.NotEmpty(t, userID)

	// Fetch OTP from Redis and verify
	otp, err := infra.Redis.Get(ctx, fmt.Sprintf("otp:%s", email)).Result()
	require.NoError(t, err)

	_, tokens, err := svc.VerifyOTP(ctx, email, otp)
	require.NoError(t, err)

	return userID, tokens
}

func TestRegister(t *testing.T) {
	svc, _ := setupAuthService(t)
	ctx := context.Background()

	userID, err := svc.Register(ctx, "testuser", "test@example.com", "password123")
	require.NoError(t, err)
	assert.NotEmpty(t, userID)
}

func TestRegisterDuplicateEmail(t *testing.T) {
	svc, _ := setupAuthService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "user1", "dup@example.com", "pass123")
	require.NoError(t, err)

	_, err = svc.Register(ctx, "user2", "dup@example.com", "pass123")
	assert.Error(t, err)
}

func TestRegisterDuplicateUsername(t *testing.T) {
	svc, _ := setupAuthService(t)
	ctx := context.Background()

	_, err := svc.Register(ctx, "sameuser", "a@example.com", "pass123")
	require.NoError(t, err)

	_, err = svc.Register(ctx, "sameuser", "b@example.com", "pass123")
	assert.Error(t, err)
}

func TestLogin(t *testing.T) {
	svc, infra := setupAuthService(t)
	ctx := context.Background()

	registerAndVerify(t, svc, infra, "loginuser", "login@example.com", "mypassword")

	userID, tokens, err := svc.Login(ctx, "login@example.com", "mypassword")
	require.NoError(t, err)
	assert.NotEmpty(t, userID)
	assert.NotEmpty(t, tokens.AccessToken)
	assert.NotEmpty(t, tokens.RefreshToken)
}

func TestLoginWrongPassword(t *testing.T) {
	svc, infra := setupAuthService(t)
	ctx := context.Background()

	registerAndVerify(t, svc, infra, "wrongpass", "wrong@example.com", "correctpass")

	_, _, err := svc.Login(ctx, "wrong@example.com", "incorrectpass")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestLoginNonExistentUser(t *testing.T) {
	svc, _ := setupAuthService(t)
	ctx := context.Background()

	_, _, err := svc.Login(ctx, "noone@example.com", "pass")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestRefreshToken(t *testing.T) {
	svc, infra := setupAuthService(t)
	ctx := context.Background()

	_, tokens := registerAndVerify(t, svc, infra, "refreshuser", "refresh@example.com", "pass123")

	newTokens, err := svc.RefreshToken(ctx, tokens.RefreshToken)
	require.NoError(t, err)
	assert.NotEmpty(t, newTokens.AccessToken)
	assert.NotEmpty(t, newTokens.RefreshToken)
	assert.NotEqual(t, tokens.RefreshToken, newTokens.RefreshToken)
}

func TestRefreshTokenInvalid(t *testing.T) {
	svc, _ := setupAuthService(t)
	ctx := context.Background()

	_, err := svc.RefreshToken(ctx, "invalid-token")
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestRefreshTokenUsedTwice(t *testing.T) {
	svc, infra := setupAuthService(t)
	ctx := context.Background()

	_, tokens := registerAndVerify(t, svc, infra, "doubleref", "double@example.com", "pass123")

	_, err := svc.RefreshToken(ctx, tokens.RefreshToken)
	require.NoError(t, err)

	// Old refresh token should be invalid now (rotation)
	_, err = svc.RefreshToken(ctx, tokens.RefreshToken)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestLogout(t *testing.T) {
	svc, infra := setupAuthService(t)
	ctx := context.Background()

	_, tokens := registerAndVerify(t, svc, infra, "logoutuser", "logout@example.com", "pass123")

	err := svc.Logout(ctx, tokens.RefreshToken)
	require.NoError(t, err)

	// Refresh token should be invalid after logout
	_, err = svc.RefreshToken(ctx, tokens.RefreshToken)
	assert.ErrorIs(t, err, ErrInvalidToken)
}

func TestValidateToken(t *testing.T) {
	svc, infra := setupAuthService(t)
	ctx := context.Background()

	_, tokens := registerAndVerify(t, svc, infra, "validateuser", "validate@example.com", "pass123")

	userID, valid, err := svc.ValidateToken(ctx, tokens.AccessToken)
	require.NoError(t, err)
	assert.True(t, valid)
	assert.NotEmpty(t, userID)
}

func TestValidateTokenInvalid(t *testing.T) {
	svc, _ := setupAuthService(t)
	ctx := context.Background()

	_, valid, err := svc.ValidateToken(ctx, "garbage-token")
	require.NoError(t, err)
	assert.False(t, valid)
}
