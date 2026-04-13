package voice

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

	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
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

// setupVoiceServer spins up Redis, builds the full handler stack,
// starts a gRPC server with auth interceptor, and returns a connected client.
func setupVoiceServer(t *testing.T) voicev1.VoiceServiceClient {
	t.Helper()

	rdb := setupRedis(t)

	voiceCfg := config.VoiceConfig{
		UDPHost: "127.0.0.1",
		UDPPort: 50052,
	}
	svc := NewService(rdb, voiceCfg, nil)
	handler := NewHandler(svc)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(middleware.AuthInterceptor(testSecret)),
	)
	voicev1.RegisterVoiceServiceServer(srv, handler)

	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return voicev1.NewVoiceServiceClient(conn)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHandler_JoinChannel(t *testing.T) {
	client := setupVoiceServer(t)

	userID := uuid.New().String()
	channelID := uuid.New().String()
	guildID := uuid.New().String()
	ctx := authCtx(userID)

	resp, err := client.JoinChannel(ctx, &voicev1.JoinChannelRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.SessionId)
	assert.Equal(t, "127.0.0.1", resp.UdpEndpoint)
	assert.Equal(t, int32(50052), resp.UdpPort)
	assert.Len(t, resp.EncryptionKey, 32)
	assert.NotZero(t, resp.Ssrc)
}

func TestHandler_GetChannelParticipants(t *testing.T) {
	client := setupVoiceServer(t)

	channelID := uuid.New().String()
	guildID := uuid.New().String()

	user1 := uuid.New().String()
	user2 := uuid.New().String()

	// Join two users.
	_, err := client.JoinChannel(authCtx(user1), &voicev1.JoinChannelRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	_, err = client.JoinChannel(authCtx(user2), &voicev1.JoinChannelRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	// Get participants.
	resp, err := client.GetChannelParticipants(authCtx(user1), &voicev1.GetChannelParticipantsRequest{
		ChannelId: channelID,
	})
	require.NoError(t, err)
	require.Len(t, resp.Participants, 2)

	userIDs := make(map[string]bool)
	for _, p := range resp.Participants {
		userIDs[p.UserId] = true
		assert.NotEmpty(t, p.SessionId)
		assert.NotZero(t, p.Ssrc)
	}
	assert.True(t, userIDs[user1])
	assert.True(t, userIDs[user2])
}

func TestHandler_GetChannelParticipants_Empty(t *testing.T) {
	client := setupVoiceServer(t)
	ctx := authCtx(uuid.New().String())

	resp, err := client.GetChannelParticipants(ctx, &voicev1.GetChannelParticipantsRequest{
		ChannelId: uuid.New().String(),
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Participants)
}

func TestHandler_UpdateVoiceState(t *testing.T) {
	client := setupVoiceServer(t)

	userID := uuid.New().String()
	channelID := uuid.New().String()
	guildID := uuid.New().String()
	ctx := authCtx(userID)

	// Must join first.
	_, err := client.JoinChannel(ctx, &voicev1.JoinChannelRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	// Update voice state.
	_, err = client.UpdateVoiceState(ctx, &voicev1.UpdateVoiceStateRequest{
		ChannelId: channelID,
		SelfMute:  true,
		SelfDeaf:  false,
		Video:     true,
		Stream:    false,
	})
	require.NoError(t, err)

	// Verify via GetChannelParticipants.
	resp, err := client.GetChannelParticipants(ctx, &voicev1.GetChannelParticipantsRequest{
		ChannelId: channelID,
	})
	require.NoError(t, err)
	require.Len(t, resp.Participants, 1)

	p := resp.Participants[0]
	assert.Equal(t, userID, p.UserId)
	assert.True(t, p.SelfMuted)
	assert.False(t, p.SelfDeafened)
	assert.True(t, p.Video)
	assert.False(t, p.Streaming)
}

func TestHandler_LeaveChannel(t *testing.T) {
	client := setupVoiceServer(t)

	userID := uuid.New().String()
	channelID := uuid.New().String()
	guildID := uuid.New().String()
	ctx := authCtx(userID)

	// Join.
	_, err := client.JoinChannel(ctx, &voicev1.JoinChannelRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	// Verify joined.
	resp, err := client.GetChannelParticipants(ctx, &voicev1.GetChannelParticipantsRequest{
		ChannelId: channelID,
	})
	require.NoError(t, err)
	require.Len(t, resp.Participants, 1)

	// Leave.
	_, err = client.LeaveChannel(ctx, &voicev1.LeaveChannelRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	// Verify empty.
	resp, err = client.GetChannelParticipants(ctx, &voicev1.GetChannelParticipantsRequest{
		ChannelId: channelID,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Participants)
}

func TestHandler_UpdateVoiceState_NotInChannel(t *testing.T) {
	client := setupVoiceServer(t)

	userID := uuid.New().String()
	channelID := uuid.New().String()
	ctx := authCtx(userID)

	// UpdateVoiceState without joining should fail.
	_, err := client.UpdateVoiceState(ctx, &voicev1.UpdateVoiceStateRequest{
		ChannelId: channelID,
		SelfMute:  true,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

// ---------------------------------------------------------------------------
// Validation / error tests
// ---------------------------------------------------------------------------

func TestHandler_JoinChannel_Unauthenticated(t *testing.T) {
	client := setupVoiceServer(t)

	_, err := client.JoinChannel(context.Background(), &voicev1.JoinChannelRequest{
		GuildId:   uuid.New().String(),
		ChannelId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_JoinChannel_MissingChannelID(t *testing.T) {
	client := setupVoiceServer(t)
	ctx := authCtx(uuid.New().String())

	_, err := client.JoinChannel(ctx, &voicev1.JoinChannelRequest{
		GuildId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_LeaveChannel_Unauthenticated(t *testing.T) {
	client := setupVoiceServer(t)

	_, err := client.LeaveChannel(context.Background(), &voicev1.LeaveChannelRequest{
		ChannelId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_LeaveChannel_MissingChannelID(t *testing.T) {
	client := setupVoiceServer(t)
	ctx := authCtx(uuid.New().String())

	_, err := client.LeaveChannel(ctx, &voicev1.LeaveChannelRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_GetChannelParticipants_MissingChannelID(t *testing.T) {
	client := setupVoiceServer(t)
	ctx := authCtx(uuid.New().String())

	_, err := client.GetChannelParticipants(ctx, &voicev1.GetChannelParticipantsRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_UpdateVoiceState_Unauthenticated(t *testing.T) {
	client := setupVoiceServer(t)

	_, err := client.UpdateVoiceState(context.Background(), &voicev1.UpdateVoiceStateRequest{
		ChannelId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_UpdateVoiceState_MissingChannelID(t *testing.T) {
	client := setupVoiceServer(t)
	ctx := authCtx(uuid.New().String())

	_, err := client.UpdateVoiceState(ctx, &voicev1.UpdateVoiceStateRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_JoinAndLeaveMultipleUsers(t *testing.T) {
	client := setupVoiceServer(t)

	channelID := uuid.New().String()
	guildID := uuid.New().String()

	user1 := uuid.New().String()
	user2 := uuid.New().String()
	user3 := uuid.New().String()

	// All three join.
	for _, uid := range []string{user1, user2, user3} {
		_, err := client.JoinChannel(authCtx(uid), &voicev1.JoinChannelRequest{
			GuildId:   guildID,
			ChannelId: channelID,
		})
		require.NoError(t, err)
	}

	resp, err := client.GetChannelParticipants(authCtx(user1), &voicev1.GetChannelParticipantsRequest{
		ChannelId: channelID,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Participants, 3)

	// User2 leaves.
	_, err = client.LeaveChannel(authCtx(user2), &voicev1.LeaveChannelRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	resp, err = client.GetChannelParticipants(authCtx(user1), &voicev1.GetChannelParticipantsRequest{
		ChannelId: channelID,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Participants, 2)

	userIDs := make(map[string]bool)
	for _, p := range resp.Participants {
		userIDs[p.UserId] = true
	}
	assert.True(t, userIDs[user1])
	assert.False(t, userIDs[user2], "user2 should have left")
	assert.True(t, userIDs[user3])
}
