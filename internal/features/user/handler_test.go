package user_test

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
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	"github.com/ananddub/ndiscord_backend/internal/features/auth"
	"github.com/ananddub/ndiscord_backend/internal/features/user"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

const testJWTSecret = "test-secret"

type testClients struct {
	auth  authv1.AuthServiceClient
	user  userv1.UserServiceClient
	infra *testutil.TestInfra
}

func setupUserGRPCServer(t *testing.T) *testClients {
	t.Helper()
	infra := testutil.SetupTestInfra(t)

	// Auth stack
	authRepo := auth.NewRepository(infra.Pool, infra.Redis)
	jwtCfg := config.JWTConfig{
		Secret:          testJWTSecret,
		AccessTokenTTL:  15 * time.Minute,
		RefreshTokenTTL: 7 * 24 * time.Hour,
	}
	authSvc := auth.NewService(authRepo, jwtCfg)
	authHandler := auth.NewHandler(authSvc)

	// User stack
	userRepo := user.NewRepository(infra.Pool, infra.Redis)
	userSvc := user.NewService(userRepo, nil)
	userHandler := user.NewHandler(userSvc)

	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(middleware.AuthInterceptor(testJWTSecret)),
	)
	authv1.RegisterAuthServiceServer(srv, authHandler)
	userv1.RegisterUserServiceServer(srv, userHandler)
	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return &testClients{
		auth:  authv1.NewAuthServiceClient(conn),
		user:  userv1.NewUserServiceClient(conn),
		infra: infra,
	}
}

// createUser registers a user via the auth service, verifies via OTP, and returns (userID, accessToken).
func createUser(t *testing.T, c *testClients, username, email, password string) (string, string) {
	t.Helper()
	ctx := context.Background()

	regResp, err := c.auth.Register(ctx, &authv1.RegisterRequest{
		Username: username,
		Email:    email,
		Password: password,
	})
	require.NoError(t, err)

	// Fetch OTP from Redis and verify to get tokens
	otp, err := c.infra.Redis.Get(ctx, fmt.Sprintf("otp:%s", email)).Result()
	require.NoError(t, err)

	verifyResp, err := c.auth.VerifyOTP(ctx, &authv1.VerifyOTPRequest{
		Email: email,
		Otp:   otp,
	})
	require.NoError(t, err)

	return regResp.GetUserId(), verifyResp.GetAccessToken()
}

// authedCtx creates a context with the given access token in metadata.
func authedCtx(token string) context.Context {
	md := metadata.New(map[string]string{
		"authorization": "Bearer " + token,
	})
	return metadata.NewOutgoingContext(context.Background(), md)
}

// ---------------------------------------------------------------------------
// User profile tests
// ---------------------------------------------------------------------------

func TestUserGRPC_GetUser(t *testing.T) {
	c := setupUserGRPCServer(t)
	userID, token := createUser(t, c, "alice", "alice@example.com", "password123")

	resp, err := c.user.GetUser(authedCtx(token), &userv1.GetUserRequest{
		UserId: userID,
	})
	require.NoError(t, err)
	assert.Equal(t, userID, resp.GetUser().GetId())
	assert.Equal(t, "alice", resp.GetUser().GetUsername())
	assert.Equal(t, "alice@example.com", resp.GetUser().GetEmail())
}

func TestUserGRPC_GetUser_NotFound(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "alice", "alice@example.com", "password123")

	_, err := c.user.GetUser(authedCtx(token), &userv1.GetUserRequest{
		UserId: "00000000-0000-0000-0000-000000000000",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUserGRPC_GetUser_InvalidID(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "alice", "alice@example.com", "password123")

	_, err := c.user.GetUser(authedCtx(token), &userv1.GetUserRequest{
		UserId: "not-a-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUserGRPC_GetUser_EmptyID(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "alice", "alice@example.com", "password123")

	_, err := c.user.GetUser(authedCtx(token), &userv1.GetUserRequest{
		UserId: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUserGRPC_GetUser_Unauthenticated(t *testing.T) {
	c := setupUserGRPCServer(t)

	// No auth metadata at all
	_, err := c.user.GetUser(context.Background(), &userv1.GetUserRequest{
		UserId: "00000000-0000-0000-0000-000000000000",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestUserGRPC_UpdateUser(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "alice", "alice@example.com", "password123")

	newUsername := "alice_updated"
	newBio := "Hello, world!"
	resp, err := c.user.UpdateUser(authedCtx(token), &userv1.UpdateUserRequest{
		Username: &newUsername,
		Bio:      &newBio,
	})
	require.NoError(t, err)
	assert.Equal(t, "alice_updated", resp.GetUser().GetUsername())
	assert.Equal(t, "Hello, world!", resp.GetUser().GetBio())
}

func TestUserGRPC_UpdateUser_Unauthenticated(t *testing.T) {
	c := setupUserGRPCServer(t)

	newUsername := "hacker"
	_, err := c.user.UpdateUser(context.Background(), &userv1.UpdateUserRequest{
		Username: &newUsername,
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestUserGRPC_GetUserByUsername(t *testing.T) {
	c := setupUserGRPCServer(t)
	userID, token := createUser(t, c, "findme", "findme@example.com", "password123")

	resp, err := c.user.GetUserByUsername(authedCtx(token), &userv1.GetUserByUsernameRequest{
		Username: "findme",
	})
	require.NoError(t, err)
	assert.Equal(t, userID, resp.GetUser().GetId())
	assert.Equal(t, "findme", resp.GetUser().GetUsername())
}

func TestUserGRPC_GetUserByUsername_NotFound(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "alice", "alice@example.com", "password123")

	_, err := c.user.GetUserByUsername(authedCtx(token), &userv1.GetUserByUsernameRequest{
		Username: "nonexistent",
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUserGRPC_GetUserByUsername_Empty(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "alice", "alice@example.com", "password123")

	_, err := c.user.GetUserByUsername(authedCtx(token), &userv1.GetUserByUsernameRequest{
		Username: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUserGRPC_SearchUsers(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "searchable_alice", "searchalice@example.com", "password123")
	createUser(t, c, "searchable_bob", "searchbob@example.com", "password123")
	createUser(t, c, "other_carol", "carol@example.com", "password123")

	resp, err := c.user.SearchUsers(authedCtx(token), &userv1.SearchUsersRequest{
		Query: "searchable",
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), resp.GetTotal())
	assert.Len(t, resp.GetUsers(), 2)
}

func TestUserGRPC_SearchUsers_EmptyQuery(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "alice", "alice@example.com", "password123")

	_, err := c.user.SearchUsers(authedCtx(token), &userv1.SearchUsersRequest{
		Query: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUserGRPC_SearchUsers_NoResults(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "alice", "alice@example.com", "password123")

	resp, err := c.user.SearchUsers(authedCtx(token), &userv1.SearchUsersRequest{
		Query: "zzzznonexistentzzzz",
		Limit: 10,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(0), resp.GetTotal())
	assert.Empty(t, resp.GetUsers())
}

// ---------------------------------------------------------------------------
// Friend request tests
// ---------------------------------------------------------------------------

func TestUserGRPC_SendFriendRequest(t *testing.T) {
	c := setupUserGRPCServer(t)
	userID1, token1 := createUser(t, c, "user1", "user1@example.com", "password123")
	userID2, _ := createUser(t, c, "user2", "user2@example.com", "password123")

	resp, err := c.user.SendFriendRequest(authedCtx(token1), &userv1.SendFriendRequestRequest{
		TargetUserId: userID2,
	})
	require.NoError(t, err)
	assert.Equal(t, userID1, resp.GetFriendship().GetUserId())
	assert.Equal(t, userID2, resp.GetFriendship().GetFriendId())
	assert.Equal(t, userv1.FriendshipStatus_FRIENDSHIP_STATUS_PENDING, resp.GetFriendship().GetStatus())
}

func TestUserGRPC_SendFriendRequest_ToSelf(t *testing.T) {
	c := setupUserGRPCServer(t)
	userID1, token1 := createUser(t, c, "lonely", "lonely@example.com", "password123")

	_, err := c.user.SendFriendRequest(authedCtx(token1), &userv1.SendFriendRequestRequest{
		TargetUserId: userID1,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUserGRPC_SendFriendRequest_Duplicate(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token1 := createUser(t, c, "user1", "user1@example.com", "password123")
	userID2, _ := createUser(t, c, "user2", "user2@example.com", "password123")

	_, err := c.user.SendFriendRequest(authedCtx(token1), &userv1.SendFriendRequestRequest{
		TargetUserId: userID2,
	})
	require.NoError(t, err)

	// Sending again should fail
	_, err = c.user.SendFriendRequest(authedCtx(token1), &userv1.SendFriendRequestRequest{
		TargetUserId: userID2,
	})
	require.Error(t, err)
	assert.Equal(t, codes.AlreadyExists, status.Code(err))
}

func TestUserGRPC_SendFriendRequest_EmptyTarget(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token1 := createUser(t, c, "user1", "user1@example.com", "password123")

	_, err := c.user.SendFriendRequest(authedCtx(token1), &userv1.SendFriendRequestRequest{
		TargetUserId: "",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUserGRPC_SendFriendRequest_Unauthenticated(t *testing.T) {
	c := setupUserGRPCServer(t)

	_, err := c.user.SendFriendRequest(context.Background(), &userv1.SendFriendRequestRequest{
		TargetUserId: "00000000-0000-0000-0000-000000000000",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestUserGRPC_AcceptFriendRequest(t *testing.T) {
	c := setupUserGRPCServer(t)
	userID1, token1 := createUser(t, c, "user1", "user1@example.com", "password123")
	userID2, token2 := createUser(t, c, "user2", "user2@example.com", "password123")

	// user1 sends request to user2
	_, err := c.user.SendFriendRequest(authedCtx(token1), &userv1.SendFriendRequestRequest{
		TargetUserId: userID2,
	})
	require.NoError(t, err)

	// user2 accepts the request from user1
	resp, err := c.user.AcceptFriendRequest(authedCtx(token2), &userv1.AcceptFriendRequestRequest{
		RequesterUserId: userID1,
	})
	require.NoError(t, err)
	assert.Equal(t, userv1.FriendshipStatus_FRIENDSHIP_STATUS_ACCEPTED, resp.GetFriendship().GetStatus())
}

func TestUserGRPC_AcceptFriendRequest_NoPending(t *testing.T) {
	c := setupUserGRPCServer(t)
	userID1, _ := createUser(t, c, "user1", "user1@example.com", "password123")
	_, token2 := createUser(t, c, "user2", "user2@example.com", "password123")

	// No friend request was sent, accept should fail
	_, err := c.user.AcceptFriendRequest(authedCtx(token2), &userv1.AcceptFriendRequestRequest{
		RequesterUserId: userID1,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUserGRPC_ListFriends(t *testing.T) {
	c := setupUserGRPCServer(t)
	uid1, tok1 := createUser(t, c, "friend1", "friend1@example.com", "password123")
	uid2, tok2 := createUser(t, c, "friend2", "friend2@example.com", "password123")

	// friend1 sends request to friend2
	_, err := c.user.SendFriendRequest(authedCtx(tok1), &userv1.SendFriendRequestRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)

	// friend2 accepts
	_, err = c.user.AcceptFriendRequest(authedCtx(tok2), &userv1.AcceptFriendRequestRequest{
		RequesterUserId: uid1,
	})
	require.NoError(t, err)

	// Both should see each other in friends list
	resp1, err := c.user.ListFriends(authedCtx(tok1), &userv1.ListFriendsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp1.GetFriends(), 1)
	assert.Equal(t, uid2, resp1.GetFriends()[0].GetId())

	resp2, err := c.user.ListFriends(authedCtx(tok2), &userv1.ListFriendsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp2.GetFriends(), 1)
	assert.Equal(t, uid1, resp2.GetFriends()[0].GetId())
}

func TestUserGRPC_ListFriends_Empty(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "lonely", "lonely@example.com", "password123")

	resp, err := c.user.ListFriends(authedCtx(token), &userv1.ListFriendsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetFriends())
}

func TestUserGRPC_ListFriends_Unauthenticated(t *testing.T) {
	c := setupUserGRPCServer(t)

	_, err := c.user.ListFriends(context.Background(), &userv1.ListFriendsRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestUserGRPC_RemoveFriend(t *testing.T) {
	c := setupUserGRPCServer(t)
	uid1, tok1 := createUser(t, c, "rem1", "rem1@example.com", "password123")
	uid2, tok2 := createUser(t, c, "rem2", "rem2@example.com", "password123")

	// Become friends
	_, err := c.user.SendFriendRequest(authedCtx(tok1), &userv1.SendFriendRequestRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)
	_, err = c.user.AcceptFriendRequest(authedCtx(tok2), &userv1.AcceptFriendRequestRequest{
		RequesterUserId: uid1,
	})
	require.NoError(t, err)

	// Remove the friendship
	_, err = c.user.RemoveFriend(authedCtx(tok1), &userv1.RemoveFriendRequest{
		FriendId: uid2,
	})
	require.NoError(t, err)

	// Friends list should now be empty
	resp, err := c.user.ListFriends(authedCtx(tok1), &userv1.ListFriendsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetFriends())
}

func TestUserGRPC_RemoveFriend_NotFriends(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, tok1 := createUser(t, c, "rem1", "rem1@example.com", "password123")
	uid2, _ := createUser(t, c, "rem2", "rem2@example.com", "password123")

	_, err := c.user.RemoveFriend(authedCtx(tok1), &userv1.RemoveFriendRequest{
		FriendId: uid2,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUserGRPC_BlockUser(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, tok1 := createUser(t, c, "blocker", "blocker@example.com", "password123")
	uid2, _ := createUser(t, c, "blocked", "blocked@example.com", "password123")

	_, err := c.user.BlockUser(authedCtx(tok1), &userv1.BlockUserRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)

	// Blocked list should contain user2
	resp, err := c.user.ListBlocked(authedCtx(tok1), &userv1.ListBlockedRequest{})
	require.NoError(t, err)
	assert.Len(t, resp.GetBlocked(), 1)
	assert.Equal(t, uid2, resp.GetBlocked()[0].GetId())
}

func TestUserGRPC_BlockUser_Self(t *testing.T) {
	c := setupUserGRPCServer(t)
	uid1, tok1 := createUser(t, c, "selfblocker", "selfblock@example.com", "password123")

	_, err := c.user.BlockUser(authedCtx(tok1), &userv1.BlockUserRequest{
		TargetUserId: uid1,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUserGRPC_BlockUser_RemovesFriendship(t *testing.T) {
	c := setupUserGRPCServer(t)
	uid1, tok1 := createUser(t, c, "bf1", "bf1@example.com", "password123")
	uid2, tok2 := createUser(t, c, "bf2", "bf2@example.com", "password123")

	// Become friends first
	_, err := c.user.SendFriendRequest(authedCtx(tok1), &userv1.SendFriendRequestRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)
	_, err = c.user.AcceptFriendRequest(authedCtx(tok2), &userv1.AcceptFriendRequestRequest{
		RequesterUserId: uid1,
	})
	require.NoError(t, err)

	// Block should remove the friendship
	_, err = c.user.BlockUser(authedCtx(tok1), &userv1.BlockUserRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)

	// Friends list should be empty
	friendsResp, err := c.user.ListFriends(authedCtx(tok1), &userv1.ListFriendsRequest{})
	require.NoError(t, err)
	assert.Empty(t, friendsResp.GetFriends())

	// Blocked list should contain user2
	blockedResp, err := c.user.ListBlocked(authedCtx(tok1), &userv1.ListBlockedRequest{})
	require.NoError(t, err)
	assert.Len(t, blockedResp.GetBlocked(), 1)
}

func TestUserGRPC_UnblockUser(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, tok1 := createUser(t, c, "ublocker", "ublocker@example.com", "password123")
	uid2, _ := createUser(t, c, "ublocked", "ublocked@example.com", "password123")

	// Block first
	_, err := c.user.BlockUser(authedCtx(tok1), &userv1.BlockUserRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)

	// Unblock
	_, err = c.user.UnblockUser(authedCtx(tok1), &userv1.UnblockUserRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)

	// Blocked list should now be empty
	resp, err := c.user.ListBlocked(authedCtx(tok1), &userv1.ListBlockedRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetBlocked())
}

func TestUserGRPC_UnblockUser_NotBlocked(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, tok1 := createUser(t, c, "ublocker", "ublocker@example.com", "password123")
	uid2, _ := createUser(t, c, "ublocked", "ublocked@example.com", "password123")

	_, err := c.user.UnblockUser(authedCtx(tok1), &userv1.UnblockUserRequest{
		TargetUserId: uid2,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUserGRPC_ListPendingRequests(t *testing.T) {
	c := setupUserGRPCServer(t)
	uid1, tok1 := createUser(t, c, "pending1", "pending1@example.com", "password123")
	uid2, tok2 := createUser(t, c, "pending2", "pending2@example.com", "password123")

	// user1 sends request to user2
	_, err := c.user.SendFriendRequest(authedCtx(tok1), &userv1.SendFriendRequestRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)

	// user1 should see it as outgoing
	resp1, err := c.user.ListPendingRequests(authedCtx(tok1), &userv1.ListPendingRequestsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp1.GetOutgoing(), 1)
	assert.Equal(t, uid2, resp1.GetOutgoing()[0].GetFriendId())
	assert.Empty(t, resp1.GetIncoming())

	// user2 should see it as incoming
	resp2, err := c.user.ListPendingRequests(authedCtx(tok2), &userv1.ListPendingRequestsRequest{})
	require.NoError(t, err)
	assert.Len(t, resp2.GetIncoming(), 1)
	assert.Equal(t, uid1, resp2.GetIncoming()[0].GetUserId())
	assert.Empty(t, resp2.GetOutgoing())
}

func TestUserGRPC_DeclineFriendRequest(t *testing.T) {
	c := setupUserGRPCServer(t)
	uid1, tok1 := createUser(t, c, "decline1", "decline1@example.com", "password123")
	uid2, tok2 := createUser(t, c, "decline2", "decline2@example.com", "password123")

	// user1 sends request to user2
	_, err := c.user.SendFriendRequest(authedCtx(tok1), &userv1.SendFriendRequestRequest{
		TargetUserId: uid2,
	})
	require.NoError(t, err)

	// user2 declines
	_, err = c.user.DeclineFriendRequest(authedCtx(tok2), &userv1.DeclineFriendRequestRequest{
		RequesterUserId: uid1,
	})
	require.NoError(t, err)

	// No pending requests for either user
	resp1, err := c.user.ListPendingRequests(authedCtx(tok1), &userv1.ListPendingRequestsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp1.GetOutgoing())

	resp2, err := c.user.ListPendingRequests(authedCtx(tok2), &userv1.ListPendingRequestsRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp2.GetIncoming())
}

func TestUserGRPC_DeclineFriendRequest_NoPending(t *testing.T) {
	c := setupUserGRPCServer(t)
	uid1, _ := createUser(t, c, "decline1", "decline1@example.com", "password123")
	_, tok2 := createUser(t, c, "decline2", "decline2@example.com", "password123")

	_, err := c.user.DeclineFriendRequest(authedCtx(tok2), &userv1.DeclineFriendRequestRequest{
		RequesterUserId: uid1,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUserGRPC_DeleteUser(t *testing.T) {
	c := setupUserGRPCServer(t)
	userID, token := createUser(t, c, "todelete", "todelete@example.com", "password123")

	_, err := c.user.DeleteUser(authedCtx(token), &userv1.DeleteUserRequest{
		UserId: userID,
	})
	require.NoError(t, err)

	// Trying to get the deleted user should fail
	_, err = c.user.GetUser(authedCtx(token), &userv1.GetUserRequest{
		UserId: userID,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUserGRPC_SendFriendRequest_ToBlockedUser(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, tok1 := createUser(t, c, "sender", "sender@example.com", "password123")
	uid2, tok2 := createUser(t, c, "target", "target@example.com", "password123")

	// user2 blocks user1 -- get user1's ID first
	uid1Resp, err := c.user.GetUserByUsername(authedCtx(tok1), &userv1.GetUserByUsernameRequest{
		Username: "sender",
	})
	require.NoError(t, err)
	uid1 := uid1Resp.GetUser().GetId()

	_, err = c.user.BlockUser(authedCtx(tok2), &userv1.BlockUserRequest{
		TargetUserId: uid1,
	})
	require.NoError(t, err)

	// user1 tries to send friend request to user2 -- should fail due to block
	_, err = c.user.SendFriendRequest(authedCtx(tok1), &userv1.SendFriendRequestRequest{
		TargetUserId: uid2,
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestUserGRPC_ListBlocked_Empty(t *testing.T) {
	c := setupUserGRPCServer(t)
	_, token := createUser(t, c, "nobody", "nobody@example.com", "password123")

	resp, err := c.user.ListBlocked(authedCtx(token), &userv1.ListBlockedRequest{})
	require.NoError(t, err)
	assert.Empty(t, resp.GetBlocked())
}

func TestUserGRPC_ListBlocked_Unauthenticated(t *testing.T) {
	c := setupUserGRPCServer(t)

	_, err := c.user.ListBlocked(context.Background(), &userv1.ListBlockedRequest{})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}
