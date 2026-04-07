package guild_test

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

	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	"github.com/ananddub/ndiscord_backend/internal/features/guild"
	"github.com/ananddub/ndiscord_backend/internal/shared/event"
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

// setupGuildServer starts a real gRPC server with the guild handler and returns a client.
func setupGuildServer(t *testing.T, infra *testutil.TestInfra) guildv1.GuildServiceClient {
	t.Helper()

	repo := guild.NewRepository(infra.Pool, infra.Redis)
	svc := guild.NewService(repo)
	guildSubs := event.NewSubscriptionManager[*guildv1.GuildEvent]()
	handler := guild.NewHandler(svc, guildSubs)

	srv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(middleware.AuthInterceptor(jwtSecret)),
	)
	guildv1.RegisterGuildServiceServer(srv, handler)

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

	return guildv1.NewGuildServiceClient(conn)
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

// createTestChannel inserts a channel directly via SQL for a given guild and returns its UUID string.
func createTestChannel(t *testing.T, infra *testutil.TestInfra, guildID string) string {
	t.Helper()
	id := uuid.New().String()
	_, err := infra.Pool.Exec(context.Background(),
		`INSERT INTO channels (id, guild_id, name, type) VALUES ($1, $2, $3, $4)`,
		id, guildID, "general", 1,
	)
	require.NoError(t, err)
	return id
}

func TestGuildHandler_FullFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	infra := testutil.SetupTestInfra(t)
	client := setupGuildServer(t, infra)

	ownerID := createTestUser(t, infra, "owner_user")
	user2ID := createTestUser(t, infra, "second_user")
	user3ID := createTestUser(t, infra, "third_user")

	ownerCtx := authCtx(ownerID)
	user2Ctx := authCtx(user2ID)

	// ---- 1. CreateGuild ----
	createResp, err := client.CreateGuild(ownerCtx, &guildv1.CreateGuildRequest{
		Name:        "Test Guild",
		Description: "A guild for testing",
	})
	require.NoError(t, err)
	require.NotNil(t, createResp.GetGuild())

	g := createResp.GetGuild()
	assert.Equal(t, "Test Guild", g.GetName())
	assert.Equal(t, ownerID, g.GetOwnerId())
	assert.Equal(t, int32(1), g.GetMemberCount())

	guildID := g.GetId()

	// ---- 2. GetGuild ----
	getResp, err := client.GetGuild(ownerCtx, &guildv1.GetGuildRequest{GuildId: guildID})
	require.NoError(t, err)
	assert.Equal(t, "Test Guild", getResp.GetGuild().GetName())
	assert.Equal(t, ownerID, getResp.GetGuild().GetOwnerId())
	assert.Equal(t, int32(1), getResp.GetGuild().GetMemberCount())

	// ---- 3. UpdateGuild ----
	newName := "Updated Guild"
	updateResp, err := client.UpdateGuild(ownerCtx, &guildv1.UpdateGuildRequest{
		GuildId: guildID,
		Name:    &newName,
	})
	require.NoError(t, err)
	assert.Equal(t, "Updated Guild", updateResp.GetGuild().GetName())

	// ---- 4. ListUserGuilds ----
	listResp, err := client.ListUserGuilds(ownerCtx, &guildv1.ListUserGuildsRequest{})
	require.NoError(t, err)
	require.Len(t, listResp.GetGuilds(), 1)
	assert.Equal(t, guildID, listResp.GetGuilds()[0].GetId())

	// ---- 5. CreateInvite (requires a channel) ----
	channelID := createTestChannel(t, infra, guildID)

	inviteResp, err := client.CreateInvite(ownerCtx, &guildv1.CreateInviteRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)
	require.NotEmpty(t, inviteResp.GetInvite().GetCode())
	inviteCode := inviteResp.GetInvite().GetCode()

	// ---- 6. JoinGuild (2nd user) ----
	joinResp, err := client.JoinGuild(user2Ctx, &guildv1.JoinGuildRequest{
		InviteCode: inviteCode,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), joinResp.GetGuild().GetMemberCount())

	// ---- 7. ListMembers ----
	membersResp, err := client.ListMembers(ownerCtx, &guildv1.ListMembersRequest{
		GuildId: guildID,
	})
	require.NoError(t, err)
	assert.Len(t, membersResp.GetMembers(), 2)

	memberIDs := make(map[string]bool)
	for _, m := range membersResp.GetMembers() {
		memberIDs[m.GetUserId()] = true
	}
	assert.True(t, memberIDs[ownerID], "owner should be a member")
	assert.True(t, memberIDs[user2ID], "user2 should be a member")

	// ---- 8. KickMember ----
	_, err = client.KickMember(ownerCtx, &guildv1.KickMemberRequest{
		GuildId: guildID,
		UserId:  user2ID,
	})
	require.NoError(t, err)

	membersResp, err = client.ListMembers(ownerCtx, &guildv1.ListMembersRequest{
		GuildId: guildID,
	})
	require.NoError(t, err)
	assert.Len(t, membersResp.GetMembers(), 1)

	// ---- 9. BanMember ----
	// First, rejoin user2 via a new invite
	inviteResp2, err := client.CreateInvite(ownerCtx, &guildv1.CreateInviteRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	_, err = client.JoinGuild(user2Ctx, &guildv1.JoinGuildRequest{
		InviteCode: inviteResp2.GetInvite().GetCode(),
	})
	require.NoError(t, err)

	// Ban user2
	_, err = client.BanMember(ownerCtx, &guildv1.BanMemberRequest{
		GuildId: guildID,
		UserId:  user2ID,
		Reason:  "test ban",
	})
	require.NoError(t, err)

	// Verify banned user can't rejoin
	inviteResp3, err := client.CreateInvite(ownerCtx, &guildv1.CreateInviteRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	_, err = client.JoinGuild(user2Ctx, &guildv1.JoinGuildRequest{
		InviteCode: inviteResp3.GetInvite().GetCode(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// ---- 10. Roles: Create, Assign, Remove, Delete ----
	// Add user3 to the guild for role testing
	inviteResp4, err := client.CreateInvite(ownerCtx, &guildv1.CreateInviteRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)

	user3Ctx := authCtx(user3ID)
	_, err = client.JoinGuild(user3Ctx, &guildv1.JoinGuildRequest{
		InviteCode: inviteResp4.GetInvite().GetCode(),
	})
	require.NoError(t, err)

	// CreateRole
	roleResp, err := client.CreateRole(ownerCtx, &guildv1.CreateRoleRequest{
		GuildId:     guildID,
		Name:        "Moderator",
		Color:       "#FF0000",
		Permissions: 42,
	})
	require.NoError(t, err)
	require.NotNil(t, roleResp.GetRole())
	assert.Equal(t, "Moderator", roleResp.GetRole().GetName())
	assert.Equal(t, "#FF0000", roleResp.GetRole().GetColor())
	assert.Equal(t, int64(42), roleResp.GetRole().GetPermissions())
	roleID := roleResp.GetRole().GetId()

	// AssignRole
	_, err = client.AssignRole(ownerCtx, &guildv1.AssignRoleRequest{
		GuildId: guildID,
		UserId:  user3ID,
		RoleId:  roleID,
	})
	require.NoError(t, err)

	// RemoveRole
	_, err = client.RemoveRole(ownerCtx, &guildv1.RemoveRoleRequest{
		GuildId: guildID,
		UserId:  user3ID,
		RoleId:  roleID,
	})
	require.NoError(t, err)

	// DeleteRole
	_, err = client.DeleteRole(ownerCtx, &guildv1.DeleteRoleRequest{
		GuildId: guildID,
		RoleId:  roleID,
	})
	require.NoError(t, err)

	// ---- 11. DeleteGuild ----
	_, err = client.DeleteGuild(ownerCtx, &guildv1.DeleteGuildRequest{
		GuildId: guildID,
	})
	require.NoError(t, err)

	// Verify guild is gone
	_, err = client.GetGuild(ownerCtx, &guildv1.GetGuildRequest{GuildId: guildID})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestGuildHandler_ErrorCases(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	infra := testutil.SetupTestInfra(t)
	client := setupGuildServer(t, infra)

	ownerID := createTestUser(t, infra, "error_owner")
	user2ID := createTestUser(t, infra, "error_user2")

	ownerCtx := authCtx(ownerID)
	user2Ctx := authCtx(user2ID)

	// Create a guild
	createResp, err := client.CreateGuild(ownerCtx, &guildv1.CreateGuildRequest{
		Name: "Error Test Guild",
	})
	require.NoError(t, err)
	guildID := createResp.GetGuild().GetId()
	channelID := createTestChannel(t, infra, guildID)

	// Join user2
	invResp, err := client.CreateInvite(ownerCtx, &guildv1.CreateInviteRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)
	_, err = client.JoinGuild(user2Ctx, &guildv1.JoinGuildRequest{
		InviteCode: invResp.GetInvite().GetCode(),
	})
	require.NoError(t, err)

	// Non-owner cannot delete guild
	_, err = client.DeleteGuild(user2Ctx, &guildv1.DeleteGuildRequest{
		GuildId: guildID,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// Non-owner cannot update guild
	newName := "Hacked"
	_, err = client.UpdateGuild(user2Ctx, &guildv1.UpdateGuildRequest{
		GuildId: guildID,
		Name:    &newName,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// Owner cannot leave guild
	_, err = client.LeaveGuild(ownerCtx, &guildv1.LeaveGuildRequest{
		GuildId: guildID,
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	// Non-owner cannot kick
	_, err = client.KickMember(user2Ctx, &guildv1.KickMemberRequest{
		GuildId: guildID,
		UserId:  ownerID,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// Non-owner cannot ban
	_, err = client.BanMember(user2Ctx, &guildv1.BanMemberRequest{
		GuildId: guildID,
		UserId:  ownerID,
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// Non-owner cannot create role
	_, err = client.CreateRole(user2Ctx, &guildv1.CreateRoleRequest{
		GuildId: guildID,
		Name:    "EvilRole",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// Create guild with empty name
	_, err = client.CreateGuild(ownerCtx, &guildv1.CreateGuildRequest{
		Name: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// GetGuild with empty guild_id
	_, err = client.GetGuild(ownerCtx, &guildv1.GetGuildRequest{GuildId: ""})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// Already a member can't join again
	invResp2, err := client.CreateInvite(ownerCtx, &guildv1.CreateInviteRequest{
		GuildId:   guildID,
		ChannelId: channelID,
	})
	require.NoError(t, err)
	_, err = client.JoinGuild(user2Ctx, &guildv1.JoinGuildRequest{
		InviteCode: invResp2.GetInvite().GetCode(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	// Unauthenticated request
	noAuthCtx := context.Background()
	_, err = client.CreateGuild(noAuthCtx, &guildv1.CreateGuildRequest{Name: "NoAuth"})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}
