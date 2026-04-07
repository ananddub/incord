package channel

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

// setupChannelTest returns a Repository (for guild-channel tests that trigger event publishing)
// and a Service with nil producer (safe for DM tests which don't publish events).
func setupChannelTest(t *testing.T) (*Repository, *Service, *testutil.TestInfra) {
	t.Helper()
	infra := testutil.SetupTestInfra(t)
	repo := NewRepository(infra.Pool)
	svc := NewService(repo, nil) // nil producer -- only use svc for non-publishing paths
	return repo, svc, infra
}

// createTestUser inserts a user via SQL and returns its pgtype.UUID.
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

// createTestGuildWithMember creates a guild and adds the user as a member. Returns guild ID.
func createTestGuildWithMember(t *testing.T, infra *testutil.TestInfra, ownerID pgtype.UUID) pgtype.UUID {
	t.Helper()
	ctx := context.Background()
	var guildID pgtype.UUID
	err := infra.Pool.QueryRow(ctx,
		"INSERT INTO guilds (name, description, icon_url, owner_id) VALUES ($1, $2, $3, $4) RETURNING id",
		"Test Guild", "", "", ownerID,
	).Scan(&guildID)
	require.NoError(t, err)

	_, err = infra.Pool.Exec(ctx,
		"INSERT INTO guild_members (guild_id, user_id, nickname) VALUES ($1, $2, $3)",
		guildID, ownerID, "",
	)
	require.NoError(t, err)
	return guildID
}

// --- Repository-level tests for guild channels (avoids nil producer dereference) ---

func TestCreateAndGetChannel(t *testing.T) {
	repo, _, infra := setupChannelTest(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	guildID := createTestGuildWithMember(t, infra, owner)

	ch, err := repo.CreateChannel(ctx, db.CreateChannelParams{
		GuildID: guildID,
		Name:    "general",
		Type:    1,
		Topic:   "General chat",
	})
	require.NoError(t, err)
	assert.Equal(t, "general", ch.Name)
	assert.Equal(t, int32(1), ch.Type)
	assert.True(t, ch.ID.Valid)

	// Fetch it back
	fetched, err := repo.GetChannelByID(ctx, ch.ID)
	require.NoError(t, err)
	assert.Equal(t, ch.ID, fetched.ID)
	assert.Equal(t, "General chat", fetched.Topic)
}

func TestUpdateChannel(t *testing.T) {
	repo, _, infra := setupChannelTest(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	guildID := createTestGuildWithMember(t, infra, owner)

	ch, err := repo.CreateChannel(ctx, db.CreateChannelParams{
		GuildID: guildID,
		Name:    "old-name",
		Type:    1,
	})
	require.NoError(t, err)

	newName := "new-name"
	newTopic := "updated topic"
	updated, err := repo.UpdateChannel(ctx, db.UpdateChannelParams{
		ID:    ch.ID,
		Name:  &newName,
		Topic: &newTopic,
	})
	require.NoError(t, err)
	assert.Equal(t, "new-name", updated.Name)
	assert.Equal(t, "updated topic", updated.Topic)
}

func TestDeleteChannel(t *testing.T) {
	repo, _, infra := setupChannelTest(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	guildID := createTestGuildWithMember(t, infra, owner)

	ch, err := repo.CreateChannel(ctx, db.CreateChannelParams{
		GuildID: guildID,
		Name:    "to-delete",
		Type:    1,
	})
	require.NoError(t, err)

	err = repo.DeleteChannel(ctx, ch.ID)
	require.NoError(t, err)

	_, err = repo.GetChannelByID(ctx, ch.ID)
	assert.Error(t, err)
}

func TestListGuildChannels(t *testing.T) {
	repo, _, infra := setupChannelTest(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	guildID := createTestGuildWithMember(t, infra, owner)

	_, err := repo.CreateChannel(ctx, db.CreateChannelParams{GuildID: guildID, Name: "ch1", Type: 1})
	require.NoError(t, err)
	_, err = repo.CreateChannel(ctx, db.CreateChannelParams{GuildID: guildID, Name: "ch2", Type: 1})
	require.NoError(t, err)

	// Also use service-level ListGuildChannels (does not publish events)
	channels, err := repo.ListGuildChannels(ctx, guildID)
	require.NoError(t, err)
	assert.Len(t, channels, 2)
}

// --- Service-level DM tests (no event publishing involved) ---

func TestCreateDMChannel(t *testing.T) {
	_, svc, infra := setupChannelTest(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")

	aliceStr := uuidToString(alice)
	bobStr := uuidToString(bob)

	ch, err := svc.CreateDMChannel(ctx, aliceStr, []string{bobStr})
	require.NoError(t, err)
	assert.Equal(t, int32(5), ch.Type) // DM type
	assert.True(t, ch.ID.Valid)
}

func TestCreateDMChannelIdempotent(t *testing.T) {
	_, svc, infra := setupChannelTest(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")

	aliceStr := uuidToString(alice)
	bobStr := uuidToString(bob)

	ch1, err := svc.CreateDMChannel(ctx, aliceStr, []string{bobStr})
	require.NoError(t, err)

	// Creating again should return the same channel
	ch2, err := svc.CreateDMChannel(ctx, aliceStr, []string{bobStr})
	require.NoError(t, err)
	assert.Equal(t, ch1.ID, ch2.ID)
}

func TestCreateGroupDMChannel(t *testing.T) {
	_, svc, infra := setupChannelTest(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")
	charlie := createTestUser(t, infra, "charlie", "charlie@test.com")

	aliceStr := uuidToString(alice)
	bobStr := uuidToString(bob)
	charlieStr := uuidToString(charlie)

	ch, err := svc.CreateDMChannel(ctx, aliceStr, []string{bobStr, charlieStr})
	require.NoError(t, err)
	assert.Equal(t, int32(6), ch.Type) // Group DM type
}

func TestListDMChannels(t *testing.T) {
	_, svc, infra := setupChannelTest(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	bob := createTestUser(t, infra, "bob", "bob@test.com")
	charlie := createTestUser(t, infra, "charlie", "charlie@test.com")

	aliceStr := uuidToString(alice)
	bobStr := uuidToString(bob)
	charlieStr := uuidToString(charlie)

	_, err := svc.CreateDMChannel(ctx, aliceStr, []string{bobStr})
	require.NoError(t, err)
	_, err = svc.CreateDMChannel(ctx, aliceStr, []string{charlieStr})
	require.NoError(t, err)

	channels, err := svc.ListDMChannels(ctx, aliceStr)
	require.NoError(t, err)
	assert.Len(t, channels, 2)
}

func TestCreateDMChannelNoRecipient(t *testing.T) {
	_, svc, infra := setupChannelTest(t)
	ctx := context.Background()

	alice := createTestUser(t, infra, "alice", "alice@test.com")
	aliceStr := uuidToString(alice)

	_, err := svc.CreateDMChannel(ctx, aliceStr, []string{})
	assert.ErrorIs(t, err, ErrRecipientRequired)
}

// --- Service-level GetChannel (no event publishing) ---

func TestGetChannelViaService(t *testing.T) {
	repo, svc, infra := setupChannelTest(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	guildID := createTestGuildWithMember(t, infra, owner)

	ch, err := repo.CreateChannel(ctx, db.CreateChannelParams{
		GuildID: guildID,
		Name:    "test-ch",
		Type:    1,
	})
	require.NoError(t, err)

	fetched, err := svc.GetChannel(ctx, uuidToString(ch.ID))
	require.NoError(t, err)
	assert.Equal(t, "test-ch", fetched.Name)
}

func TestGetChannelNotFound(t *testing.T) {
	_, svc, _ := setupChannelTest(t)
	ctx := context.Background()

	_, err := svc.GetChannel(ctx, "00000000-0000-0000-0000-000000000001")
	assert.ErrorIs(t, err, ErrChannelNotFound)
}

func TestGetChannelInvalidUUID(t *testing.T) {
	_, svc, _ := setupChannelTest(t)
	ctx := context.Background()

	_, err := svc.GetChannel(ctx, "not-a-uuid")
	assert.ErrorIs(t, err, ErrInvalidUUID)
}
