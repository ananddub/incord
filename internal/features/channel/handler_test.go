package channel_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	"github.com/ananddub/ndiscord_backend/internal/features/channel"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

const jwtSecret = "test-secret"

func makeToken(userID string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	s, _ := token.SignedString([]byte(jwtSecret))
	return s
}

func authCtx(userID string) context.Context {
	md := metadata.New(map[string]string{
		"authorization": "Bearer " + makeToken(userID),
	})
	return metadata.NewOutgoingContext(context.Background(), md)
}

// setupChannelServer starts a real gRPC server with the channel handler and returns a client.
func setupChannelServer(t *testing.T, infra *testutil.TestInfra) channelv1.ChannelServiceClient {
	t.Helper()

	repo := channel.NewRepository(infra.Pool)
	svc := channel.NewService(repo, nil)
	handler := channel.NewHandler(svc)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(middleware.AuthInterceptor(jwtSecret)),
	)
	channelv1.RegisterChannelServiceServer(srv, handler)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.GracefulStop)

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return channelv1.NewChannelServiceClient(conn)
}

// createTestUser inserts a user directly via SQL and returns its UUID string.
func createTestUser(t *testing.T, infra *testutil.TestInfra, username string) string {
	t.Helper()
	id := uuid.New().String()
	_, err := infra.Pool.Exec(context.Background(),
		`INSERT INTO users (id, username, email, password_hash) VALUES ($1, $2, $3, $4)`,
		id, username, username+"@test.com", "hashed_password",
	)
	require.NoError(t, err)
	return id
}

// createTestGuild inserts a guild and adds the owner as a member, returns the guild UUID string.
func createTestGuild(t *testing.T, infra *testutil.TestInfra, ownerID, name string) string {
	t.Helper()
	id := uuid.New().String()
	_, err := infra.Pool.Exec(context.Background(),
		`INSERT INTO guilds (id, name, owner_id) VALUES ($1, $2, $3)`,
		id, name, ownerID,
	)
	require.NoError(t, err)

	// Add owner as guild member
	_, err = infra.Pool.Exec(context.Background(),
		`INSERT INTO guild_members (guild_id, user_id) VALUES ($1, $2)`,
		id, ownerID,
	)
	require.NoError(t, err)
	return id
}

// addGuildMember adds a user to a guild as a member.
func addGuildMember(t *testing.T, infra *testutil.TestInfra, guildID, userID string) {
	t.Helper()
	_, err := infra.Pool.Exec(context.Background(),
		`INSERT INTO guild_members (guild_id, user_id) VALUES ($1, $2)`,
		guildID, userID,
	)
	require.NoError(t, err)
}

func TestChannelHandler_GuildChannels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	infra := testutil.SetupTestInfra(t)
	client := setupChannelServer(t, infra)

	ownerID := createTestUser(t, infra, "ch_owner")
	guildID := createTestGuild(t, infra, ownerID, "Channel Test Guild")
	ownerCtx := authCtx(ownerID)

	// ---- 1. CreateChannel ----
	createResp, err := client.CreateChannel(ownerCtx, &channelv1.CreateChannelRequest{
		GuildId: guildID,
		Name:    "general",
		Type:    channelv1.ChannelType_CHANNEL_TYPE_TEXT,
		Topic:   "General discussion",
	})
	require.NoError(t, err)
	require.NotNil(t, createResp.GetChannel())

	ch := createResp.GetChannel()
	assert.Equal(t, "general", ch.GetName())
	assert.Equal(t, guildID, ch.GetGuildId())
	assert.Equal(t, channelv1.ChannelType_CHANNEL_TYPE_TEXT, ch.GetType())
	assert.Equal(t, "General discussion", ch.GetTopic())
	channelID := ch.GetId()

	// Create a second channel
	createResp2, err := client.CreateChannel(ownerCtx, &channelv1.CreateChannelRequest{
		GuildId: guildID,
		Name:    "announcements",
		Type:    channelv1.ChannelType_CHANNEL_TYPE_TEXT,
	})
	require.NoError(t, err)
	channel2ID := createResp2.GetChannel().GetId()

	// ---- 2. ListGuildChannels ----
	listResp, err := client.ListGuildChannels(ownerCtx, &channelv1.ListGuildChannelsRequest{
		GuildId: guildID,
	})
	require.NoError(t, err)
	assert.Len(t, listResp.GetChannels(), 2)

	channelNames := make(map[string]bool)
	for _, c := range listResp.GetChannels() {
		channelNames[c.GetName()] = true
	}
	assert.True(t, channelNames["general"])
	assert.True(t, channelNames["announcements"])

	// ---- 3. GetChannel ----
	getResp, err := client.GetChannel(ownerCtx, &channelv1.GetChannelRequest{
		ChannelId: channelID,
	})
	require.NoError(t, err)
	assert.Equal(t, "general", getResp.GetChannel().GetName())
	assert.Equal(t, guildID, getResp.GetChannel().GetGuildId())

	// ---- 4. UpdateChannel ----
	newName := "general-chat"
	newTopic := "Updated topic"
	updateResp, err := client.UpdateChannel(ownerCtx, &channelv1.UpdateChannelRequest{
		ChannelId: channelID,
		Name:      &newName,
		Topic:     &newTopic,
	})
	require.NoError(t, err)
	assert.Equal(t, "general-chat", updateResp.GetChannel().GetName())
	assert.Equal(t, "Updated topic", updateResp.GetChannel().GetTopic())

	// ---- 5. DeleteChannel ----
	_, err = client.DeleteChannel(ownerCtx, &channelv1.DeleteChannelRequest{
		ChannelId: channel2ID,
	})
	require.NoError(t, err)

	// Verify deleted channel is gone
	_, err = client.GetChannel(ownerCtx, &channelv1.GetChannelRequest{
		ChannelId: channel2ID,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))

	// Verify remaining channels
	listResp, err = client.ListGuildChannels(ownerCtx, &channelv1.ListGuildChannelsRequest{
		GuildId: guildID,
	})
	require.NoError(t, err)
	assert.Len(t, listResp.GetChannels(), 1)
	assert.Equal(t, "general-chat", listResp.GetChannels()[0].GetName())
}

func TestChannelHandler_DMChannels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	infra := testutil.SetupTestInfra(t)
	client := setupChannelServer(t, infra)

	user1ID := createTestUser(t, infra, "dm_user1")
	user2ID := createTestUser(t, infra, "dm_user2")
	user3ID := createTestUser(t, infra, "dm_user3")

	user1Ctx := authCtx(user1ID)

	// ---- 1. CreateDMChannel (1:1) ----
	dmResp, err := client.CreateDMChannel(user1Ctx, &channelv1.CreateDMChannelRequest{
		RecipientIds: []string{user2ID},
	})
	require.NoError(t, err)
	require.NotNil(t, dmResp.GetChannel())

	dmCh := dmResp.GetChannel()
	assert.Equal(t, channelv1.ChannelType_CHANNEL_TYPE_DM, dmCh.GetType())
	dmChannelID := dmCh.GetId()

	// ---- 2. Creating same DM again returns existing channel ----
	dmResp2, err := client.CreateDMChannel(user1Ctx, &channelv1.CreateDMChannelRequest{
		RecipientIds: []string{user2ID},
	})
	require.NoError(t, err)
	assert.Equal(t, dmChannelID, dmResp2.GetChannel().GetId(), "should return existing DM channel")

	// ---- 3. CreateDMChannel (group DM) ----
	groupResp, err := client.CreateDMChannel(user1Ctx, &channelv1.CreateDMChannelRequest{
		RecipientIds: []string{user2ID, user3ID},
	})
	require.NoError(t, err)
	assert.Equal(t, channelv1.ChannelType_CHANNEL_TYPE_GROUP_DM, groupResp.GetChannel().GetType())

	// ---- 4. ListDMChannels ----
	listResp, err := client.ListDMChannels(user1Ctx, &channelv1.ListDMChannelsRequest{})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(listResp.GetChannels()), 2, "should have at least the 1:1 DM and group DM")
}

func TestChannelHandler_ErrorCases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	infra := testutil.SetupTestInfra(t)
	client := setupChannelServer(t, infra)

	userID := createTestUser(t, infra, "ch_err_user")
	userCtx := authCtx(userID)

	// CreateChannel without name
	_, err := client.CreateChannel(userCtx, &channelv1.CreateChannelRequest{
		GuildId: uuid.New().String(),
		Name:    "",
		Type:    channelv1.ChannelType_CHANNEL_TYPE_TEXT,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// CreateChannel for guild user is not a member of
	otherUser := createTestUser(t, infra, "ch_other_owner")
	otherGuild := createTestGuild(t, infra, otherUser, "Other Guild")
	_, err = client.CreateChannel(userCtx, &channelv1.CreateChannelRequest{
		GuildId: otherGuild,
		Name:    "sneaky",
		Type:    channelv1.ChannelType_CHANNEL_TYPE_TEXT,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// GetChannel with empty channel_id
	_, err = client.GetChannel(userCtx, &channelv1.GetChannelRequest{ChannelId: ""})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// GetChannel with non-existent channel
	_, err = client.GetChannel(userCtx, &channelv1.GetChannelRequest{ChannelId: uuid.New().String()})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))

	// UpdateChannel with empty channel_id
	_, err = client.UpdateChannel(userCtx, &channelv1.UpdateChannelRequest{ChannelId: ""})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// DeleteChannel with empty channel_id
	_, err = client.DeleteChannel(userCtx, &channelv1.DeleteChannelRequest{ChannelId: ""})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// ListGuildChannels with empty guild_id
	_, err = client.ListGuildChannels(userCtx, &channelv1.ListGuildChannelsRequest{GuildId: ""})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// CreateDMChannel with no recipients
	_, err = client.CreateDMChannel(userCtx, &channelv1.CreateDMChannelRequest{
		RecipientIds: []string{},
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// Unauthenticated CreateChannel
	noAuthCtx := context.Background()
	_, err = client.CreateChannel(noAuthCtx, &channelv1.CreateChannelRequest{
		GuildId: uuid.New().String(),
		Name:    "test",
		Type:    channelv1.ChannelType_CHANNEL_TYPE_TEXT,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// Unauthenticated CreateDMChannel
	_, err = client.CreateDMChannel(noAuthCtx, &channelv1.CreateDMChannelRequest{
		RecipientIds: []string{uuid.New().String()},
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	// Unauthenticated ListDMChannels
	_, err = client.ListDMChannels(noAuthCtx, &channelv1.ListDMChannelsRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}
