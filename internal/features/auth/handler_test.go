package auth_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	"github.com/ananddub/ndiscord_backend/internal/features/auth"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

func setupAuthGRPCServer(t *testing.T) (authv1.AuthServiceClient, *testutil.TestInfra) {
	t.Helper()
	infra := testutil.SetupTestInfra(t)

	repo := auth.NewRepository(infra.Pool, infra.Redis)
	cfg := config.JWTConfig{
		Secret:          "test-secret",
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	}
	svc := auth.NewService(repo, cfg)
	handler := auth.NewHandler(svc)

	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	authv1.RegisterAuthServiceServer(srv, handler)
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	client := authv1.NewAuthServiceClient(conn)
	return client, infra
}

// registerTestUser is a helper that registers a user and returns the RegisterResponse (user_id + message only).
func registerTestUser(t *testing.T, client authv1.AuthServiceClient, username, email, password string) *authv1.RegisterResponse {
	t.Helper()
	resp, err := client.Register(context.Background(), &authv1.RegisterRequest{
		Username: username,
		Email:    email,
		Password: password,
	})
	require.NoError(t, err)
	return resp
}

// registerAndVerifyTestUser registers a user, fetches OTP from Redis, verifies, and returns the VerifyOTPResponse (with tokens).
func registerAndVerifyTestUser(t *testing.T, client authv1.AuthServiceClient, infra *testutil.TestInfra, username, email, password string) *authv1.VerifyOTPResponse {
	t.Helper()
	ctx := context.Background()

	registerTestUser(t, client, username, email, password)

	otp, err := infra.Redis.Get(ctx, fmt.Sprintf("otp:%s", email)).Result()
	require.NoError(t, err)

	verifyResp, err := client.VerifyOTP(ctx, &authv1.VerifyOTPRequest{
		Email: email,
		Otp:   otp,
	})
	require.NoError(t, err)
	return verifyResp
}

func TestAuthGRPC_FullFlow(t *testing.T) {
	client, infra := setupAuthGRPCServer(t)
	ctx := context.Background()

	// 1. Register
	regResp, err := client.Register(ctx, &authv1.RegisterRequest{
		Username: "alice",
		Email:    "alice@example.com",
		Password: "strongpassword123",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, regResp.GetUserId())
	assert.NotEmpty(t, regResp.GetMessage())

	// 1b. Verify OTP to activate account and get tokens
	otp, err := infra.Redis.Get(ctx, "otp:alice@example.com").Result()
	require.NoError(t, err)

	verifyResp, err := client.VerifyOTP(ctx, &authv1.VerifyOTPRequest{
		Email: "alice@example.com",
		Otp:   otp,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, verifyResp.GetAccessToken())
	assert.NotEmpty(t, verifyResp.GetRefreshToken())

	// 2. Login with the same credentials
	loginResp, err := client.Login(ctx, &authv1.LoginRequest{
		Email:    "alice@example.com",
		Password: "strongpassword123",
	})
	require.NoError(t, err)
	assert.Equal(t, regResp.GetUserId(), loginResp.GetUserId())
	assert.NotEmpty(t, loginResp.GetAccessToken())
	assert.NotEmpty(t, loginResp.GetRefreshToken())

	// 3. Refresh the token
	refreshResp, err := client.RefreshToken(ctx, &authv1.RefreshTokenRequest{
		RefreshToken: loginResp.GetRefreshToken(),
	})
	require.NoError(t, err)
	assert.NotEmpty(t, refreshResp.GetAccessToken())
	assert.NotEmpty(t, refreshResp.GetRefreshToken())
	// Refresh token should be rotated
	assert.NotEqual(t, loginResp.GetRefreshToken(), refreshResp.GetRefreshToken())

	// 4. Validate the new access token
	valResp, err := client.ValidateToken(ctx, &authv1.ValidateTokenRequest{
		AccessToken: refreshResp.GetAccessToken(),
	})
	require.NoError(t, err)
	assert.True(t, valResp.GetValid())
	assert.Equal(t, regResp.GetUserId(), valResp.GetUserId())

	// 5. Logout using the current refresh token
	_, err = client.Logout(ctx, &authv1.LogoutRequest{
		RefreshToken: refreshResp.GetRefreshToken(),
	})
	require.NoError(t, err)

	// 6. Refresh should fail after logout
	_, err = client.RefreshToken(ctx, &authv1.RefreshTokenRequest{
		RefreshToken: refreshResp.GetRefreshToken(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestAuthGRPC_Register_DuplicateEmail(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	_, err := client.Register(ctx, &authv1.RegisterRequest{
		Username: "bob",
		Email:    "bob@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	// Same email again should fail
	_, err = client.Register(ctx, &authv1.RegisterRequest{
		Username: "bob2",
		Email:    "bob@example.com",
		Password: "password123",
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestAuthGRPC_Register_DuplicateUsername(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	_, err := client.Register(ctx, &authv1.RegisterRequest{
		Username: "charlie",
		Email:    "charlie@example.com",
		Password: "password123",
	})
	require.NoError(t, err)

	// Same username again should fail
	_, err = client.Register(ctx, &authv1.RegisterRequest{
		Username: "charlie",
		Email:    "charlie2@example.com",
		Password: "password123",
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestAuthGRPC_Register_EmptyFields(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	tests := []struct {
		name string
		req  *authv1.RegisterRequest
	}{
		{
			name: "empty username",
			req:  &authv1.RegisterRequest{Username: "", Email: "a@b.com", Password: "pass"},
		},
		{
			name: "empty email",
			req:  &authv1.RegisterRequest{Username: "user", Email: "", Password: "pass"},
		},
		{
			name: "empty password",
			req:  &authv1.RegisterRequest{Username: "user", Email: "a@b.com", Password: ""},
		},
		{
			name: "all empty",
			req:  &authv1.RegisterRequest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Register(ctx, tt.req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestAuthGRPC_Login_WrongPassword(t *testing.T) {
	client, infra := setupAuthGRPCServer(t)
	ctx := context.Background()

	registerAndVerifyTestUser(t, client, infra, "dave", "dave@example.com", "correctpass")

	_, err := client.Login(ctx, &authv1.LoginRequest{
		Email:    "dave@example.com",
		Password: "wrongpass",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestAuthGRPC_Login_NonexistentUser(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	_, err := client.Login(ctx, &authv1.LoginRequest{
		Email:    "nobody@example.com",
		Password: "whatever",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestAuthGRPC_Login_EmptyFields(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	tests := []struct {
		name string
		req  *authv1.LoginRequest
	}{
		{
			name: "empty email",
			req:  &authv1.LoginRequest{Email: "", Password: "pass"},
		},
		{
			name: "empty password",
			req:  &authv1.LoginRequest{Email: "a@b.com", Password: ""},
		},
		{
			name: "all empty",
			req:  &authv1.LoginRequest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := client.Login(ctx, tt.req)
			require.Error(t, err)
			assert.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

func TestAuthGRPC_RefreshToken_Invalid(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	_, err := client.RefreshToken(ctx, &authv1.RefreshTokenRequest{
		RefreshToken: "totally-invalid-refresh-token",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestAuthGRPC_RefreshToken_Empty(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	_, err := client.RefreshToken(ctx, &authv1.RefreshTokenRequest{
		RefreshToken: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAuthGRPC_RefreshToken_UsedTwice(t *testing.T) {
	client, infra := setupAuthGRPCServer(t)
	ctx := context.Background()

	verifyResp := registerAndVerifyTestUser(t, client, infra, "eve", "eve@example.com", "password123")

	// First refresh succeeds
	_, err := client.RefreshToken(ctx, &authv1.RefreshTokenRequest{
		RefreshToken: verifyResp.GetRefreshToken(),
	})
	require.NoError(t, err)

	// Second use of the same refresh token should fail (token rotation)
	_, err = client.RefreshToken(ctx, &authv1.RefreshTokenRequest{
		RefreshToken: verifyResp.GetRefreshToken(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestAuthGRPC_ValidateToken_Invalid(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	resp, err := client.ValidateToken(ctx, &authv1.ValidateTokenRequest{
		AccessToken: "not.a.valid.jwt",
	})
	require.NoError(t, err)
	assert.False(t, resp.GetValid())
	assert.Empty(t, resp.GetUserId())
}

func TestAuthGRPC_Logout_Empty(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	_, err := client.Logout(ctx, &authv1.LogoutRequest{
		RefreshToken: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestAuthGRPC_Logout_InvalidToken(t *testing.T) {
	client, _ := setupAuthGRPCServer(t)
	ctx := context.Background()

	// Logout with a made-up token should still succeed (redis DEL of non-existent key is fine)
	_, err := client.Logout(ctx, &authv1.LogoutRequest{
		RefreshToken: "nonexistent-token",
	})
	require.NoError(t, err)
}
