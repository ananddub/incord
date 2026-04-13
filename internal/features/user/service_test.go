package user

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

func setupUserService(t *testing.T) (*Service, *testutil.TestInfra) {
	t.Helper()
	infra := testutil.SetupTestInfra(t)
	repo := NewRepository(infra.Pool, infra.Redis)
	svc := NewService(repo, nil)
	return svc, infra
}

// createTestUser inserts a user directly via SQL and returns its pgtype.UUID.
func createTestUser(t *testing.T, infra *testutil.TestInfra, username, email string) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var id pgtype.UUID
	err := infra.Pool.QueryRow(ctx,
		"INSERT INTO users (username, email, password_hash) VALUES ($1, $2, $3) RETURNING id",
		username, email, "hashedpassword",
	).Scan(&id)
	require.NoError(t, err)
	return id
}

// --- GetUser ---

func TestGetUser(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	userID := createTestUser(t, infra, "alice", "alice@example.com")

	u, err := svc.GetUser(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "alice", u.Username)
	assert.Equal(t, "alice@example.com", u.Email)
}

func TestGetUserNotFound(t *testing.T) {
	svc, _ := setupUserService(t)
	ctx := context.Background()

	fakeID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	_, err := svc.GetUser(ctx, fakeID)
	assert.ErrorIs(t, err, ErrUserNotFound)
}

// --- UpdateUser ---

func TestUpdateUser(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	userID := createTestUser(t, infra, "bob", "bob@example.com")

	newBio := "Hello world"
	updated, err := svc.UpdateUser(ctx, db.UpdateUserParams{
		ID:  userID,
		Bio: &newBio,
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello world", updated.Bio)
	assert.Equal(t, "bob", updated.Username)
}

// --- SearchUsers ---

func TestSearchUsers(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	createTestUser(t, infra, "searchalice", "sa@example.com")
	createTestUser(t, infra, "searchbob", "sb@example.com")
	createTestUser(t, infra, "charlie", "c@example.com")

	users, count, err := svc.SearchUsers(ctx, "search", 10, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
	assert.Len(t, users, 2)
}

// --- Friend request flow ---

func TestSendAndAcceptFriendRequest(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")

	// Alice sends request to Bob
	friendship, err := svc.SendFriendRequest(ctx, alice, bob)
	require.NoError(t, err)
	assert.Equal(t, "pending", friendship.Status)

	// Bob accepts
	accepted, err := svc.AcceptFriendRequest(ctx, bob, alice)
	require.NoError(t, err)
	assert.Equal(t, "accepted", accepted.Status)

	// Both should see each other in friends list
	friends, err := svc.ListFriends(ctx, alice)
	require.NoError(t, err)
	assert.Len(t, friends, 1)
	assert.Equal(t, "bob", friends[0].Username)
}

func TestCannotFriendSelf(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")

	_, err := svc.SendFriendRequest(ctx, alice, alice)
	assert.ErrorIs(t, err, ErrCannotFriendSelf)
}

func TestCannotDoubleFriend(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")

	_, err := svc.SendFriendRequest(ctx, alice, bob)
	require.NoError(t, err)

	// Duplicate request
	_, err = svc.SendFriendRequest(ctx, alice, bob)
	assert.ErrorIs(t, err, ErrFriendRequestExists)
}

func TestCannotFriendIfBlocked(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")

	// Alice blocks Bob
	err := svc.BlockUser(ctx, alice, bob)
	require.NoError(t, err)

	// Alice tries to friend Bob (blocked row exists with alice as user_id)
	_, err = svc.SendFriendRequest(ctx, alice, bob)
	assert.ErrorIs(t, err, ErrUserBlocked)
}

// --- RemoveFriend ---

func TestRemoveFriend(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")

	_, err := svc.SendFriendRequest(ctx, alice, bob)
	require.NoError(t, err)
	_, err = svc.AcceptFriendRequest(ctx, bob, alice)
	require.NoError(t, err)

	err = svc.RemoveFriend(ctx, alice, bob)
	require.NoError(t, err)

	friends, err := svc.ListFriends(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, friends)
}

// --- BlockUser ---

func TestBlockUser(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")

	// Become friends first
	_, err := svc.SendFriendRequest(ctx, alice, bob)
	require.NoError(t, err)
	_, err = svc.AcceptFriendRequest(ctx, bob, alice)
	require.NoError(t, err)

	// Alice blocks Bob -- this should remove the friendship
	err = svc.BlockUser(ctx, alice, bob)
	require.NoError(t, err)

	// Friends list should be empty
	friends, err := svc.ListFriends(ctx, alice)
	require.NoError(t, err)
	assert.Empty(t, friends)

	// Blocked list should contain Bob
	blocked, err := svc.ListBlocked(ctx, alice)
	require.NoError(t, err)
	require.Len(t, blocked, 1)
	assert.Equal(t, "bob", blocked[0].Username)
}

// --- ListPendingRequests ---

func TestListPendingRequests(t *testing.T) {
	svc, infra := setupUserService(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")
	charlie := createTestUser(t, infra, "charlie", fmt.Sprintf("charlie@test.com"))

	// Bob and Charlie send requests to Alice
	_, err := svc.SendFriendRequest(ctx, bob, alice)
	require.NoError(t, err)
	_, err = svc.SendFriendRequest(ctx, charlie, alice)
	require.NoError(t, err)

	incoming, outgoing, err := svc.ListPendingRequests(ctx, alice)
	require.NoError(t, err)
	assert.Len(t, incoming, 2)
	assert.Empty(t, outgoing)

	// From Bob's perspective
	incoming, outgoing, err = svc.ListPendingRequests(ctx, bob)
	require.NoError(t, err)
	assert.Empty(t, incoming)
	assert.Len(t, outgoing, 1)
}
