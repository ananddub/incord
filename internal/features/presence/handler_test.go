package presence

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

const testSecret = "test-secret"

func init() {
	logger.Init("debug")
}

// ---------------------------------------------------------------------------
// Auth helpers
// ---------------------------------------------------------------------------

func makeToken(userID string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	s, _ := token.SignedString([]byte(testSecret))
	return s
}

func authCtx(userID string) context.Context {
	md := metadata.New(map[string]string{"authorization": "Bearer " + makeToken(userID)})
	return metadata.NewOutgoingContext(context.Background(), md)
}

// ---------------------------------------------------------------------------
// Infrastructure setup
// ---------------------------------------------------------------------------

func setupRedis(t *testing.T) *redis.Client {
	t.Helper()
	ctx := context.Background()

	redisReq := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(30 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: redisReq,
		Started:          true,
	})
	require.NoError(t, err, "failed to start redis")

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "6379")
	require.NoError(t, err)

	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", host, port.Port())})
	require.NoError(t, rdb.Ping(ctx).Err(), "failed to ping redis")

	t.Cleanup(func() {
		rdb.Close()
		_ = container.Terminate(ctx)
	})
	return rdb
}

// setupPresenceServer spins up Redis, builds the full handler
// stack, starts a gRPC server with auth interceptor, and returns a client.
func setupPresenceServer(t *testing.T) presencev1.PresenceServiceClient {
	t.Helper()

	rdb := setupRedis(t)

	svc := NewService(rdb)
	handler := NewHandler(svc)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(middleware.AuthInterceptor(testSecret)),
	)
	presencev1.RegisterPresenceServiceServer(srv, handler)

	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return presencev1.NewPresenceServiceClient(conn)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHandler_UpdatePresence(t *testing.T) {
	client := setupPresenceServer(t)

	userID := uuid.New().String()
	ctx := authCtx(userID)

	resp, err := client.UpdatePresence(ctx, &presencev1.UpdatePresenceRequest{
		Status:       presencev1.Status_STATUS_ONLINE,
		CustomStatus: "coding",
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Presence)
	assert.Equal(t, userID, resp.Presence.UserId)
	assert.Equal(t, presencev1.Status_STATUS_ONLINE, resp.Presence.Status)
	assert.Equal(t, "coding", resp.Presence.CustomStatus)
	assert.NotNil(t, resp.Presence.LastSeen)
}

func TestHandler_GetPresence_AfterUpdate(t *testing.T) {
	client := setupPresenceServer(t)

	userID := uuid.New().String()
	ctx := authCtx(userID)

	// Set presence.
	_, err := client.UpdatePresence(ctx, &presencev1.UpdatePresenceRequest{
		Status:       presencev1.Status_STATUS_DND,
		CustomStatus: "do not disturb",
	})
	require.NoError(t, err)

	// Get it back.
	resp, err := client.GetPresence(ctx, &presencev1.GetPresenceRequest{
		UserId: userID,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Presence)
	assert.Equal(t, userID, resp.Presence.UserId)
	assert.Equal(t, presencev1.Status_STATUS_DND, resp.Presence.Status)
	assert.Equal(t, "do not disturb", resp.Presence.CustomStatus)
}

func TestHandler_GetPresence_Offline(t *testing.T) {
	client := setupPresenceServer(t)
	ctx := authCtx(uuid.New().String())

	// Get presence for a user that never set it -- should return OFFLINE.
	unknownUser := uuid.New().String()
	resp, err := client.GetPresence(ctx, &presencev1.GetPresenceRequest{
		UserId: unknownUser,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Presence)
	assert.Equal(t, unknownUser, resp.Presence.UserId)
	assert.Equal(t, presencev1.Status_STATUS_OFFLINE, resp.Presence.Status)
}

func TestHandler_GetBulkPresence(t *testing.T) {
	client := setupPresenceServer(t)

	user1 := uuid.New().String()
	user2 := uuid.New().String()
	user3 := uuid.New().String() // never set -- should be OFFLINE

	// Set presence for user1 and user2.
	_, err := client.UpdatePresence(authCtx(user1), &presencev1.UpdatePresenceRequest{
		Status: presencev1.Status_STATUS_ONLINE,
	})
	require.NoError(t, err)

	_, err = client.UpdatePresence(authCtx(user2), &presencev1.UpdatePresenceRequest{
		Status:       presencev1.Status_STATUS_IDLE,
		CustomStatus: "afk",
	})
	require.NoError(t, err)

	// Bulk get.
	resp, err := client.GetBulkPresence(authCtx(user1), &presencev1.GetBulkPresenceRequest{
		UserIds: []string{user1, user2, user3},
	})
	require.NoError(t, err)
	require.Len(t, resp.Presences, 3)

	byUser := make(map[string]*presencev1.Presence)
	for _, p := range resp.Presences {
		byUser[p.UserId] = p
	}

	assert.Equal(t, presencev1.Status_STATUS_ONLINE, byUser[user1].Status)
	assert.Equal(t, presencev1.Status_STATUS_IDLE, byUser[user2].Status)
	assert.Equal(t, "afk", byUser[user2].CustomStatus)
	assert.Equal(t, presencev1.Status_STATUS_OFFLINE, byUser[user3].Status)
}

func TestHandler_UpdatePresence_StatusTransition(t *testing.T) {
	client := setupPresenceServer(t)

	userID := uuid.New().String()
	ctx := authCtx(userID)

	// Online.
	_, err := client.UpdatePresence(ctx, &presencev1.UpdatePresenceRequest{
		Status: presencev1.Status_STATUS_ONLINE,
	})
	require.NoError(t, err)

	// Transition to IDLE.
	resp, err := client.UpdatePresence(ctx, &presencev1.UpdatePresenceRequest{
		Status:       presencev1.Status_STATUS_IDLE,
		CustomStatus: "away",
	})
	require.NoError(t, err)
	assert.Equal(t, presencev1.Status_STATUS_IDLE, resp.Presence.Status)
	assert.Equal(t, "away", resp.Presence.CustomStatus)

	// Verify.
	getResp, err := client.GetPresence(ctx, &presencev1.GetPresenceRequest{
		UserId: userID,
	})
	require.NoError(t, err)
	assert.Equal(t, presencev1.Status_STATUS_IDLE, getResp.Presence.Status)
}

// ---------------------------------------------------------------------------
// Validation / error tests
// ---------------------------------------------------------------------------

func TestHandler_UpdatePresence_Unauthenticated(t *testing.T) {
	client := setupPresenceServer(t)

	_, err := client.UpdatePresence(context.Background(), &presencev1.UpdatePresenceRequest{
		Status: presencev1.Status_STATUS_ONLINE,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_GetPresence_MissingUserID(t *testing.T) {
	client := setupPresenceServer(t)
	ctx := authCtx(uuid.New().String())

	_, err := client.GetPresence(ctx, &presencev1.GetPresenceRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_GetBulkPresence_EmptyList(t *testing.T) {
	client := setupPresenceServer(t)
	ctx := authCtx(uuid.New().String())

	_, err := client.GetBulkPresence(ctx, &presencev1.GetBulkPresenceRequest{
		UserIds: []string{},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
