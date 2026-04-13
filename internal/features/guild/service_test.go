package guild

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ananddub/ndiscord_backend/gen/db"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

func setupGuildService(t *testing.T) (*Service, *testutil.TestInfra) {
	t.Helper()
	infra := testutil.SetupTestInfra(t)
	repo := NewRepository(infra.Pool, infra.Redis)
	svc := NewService(repo, nil, nil)
	return svc, infra
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

// createGuildWithChannel is a helper that creates a guild, then creates a channel in it
// (needed for invite creation, which requires a valid channel_id).
func createGuildWithChannel(t *testing.T, svc *Service, infra *testutil.TestInfra, ownerID pgtype.UUID) (db.Guild, pgtype.UUID) {
	t.Helper()
	ctx := context.Background()

	g, err := svc.CreateGuild(ctx, ownerID, "Test Guild", "A guild", "")
	require.NoError(t, err)

	// Create a channel directly via SQL
	var channelID pgtype.UUID
	err = infra.Pool.QueryRow(ctx,
		"INSERT INTO channels (guild_id, name, type) VALUES ($1, $2, $3) RETURNING id",
		g.ID, "general", 1,
	).Scan(&channelID)
	require.NoError(t, err)

	return g, channelID
}

// --- CreateGuild ---

func TestCreateGuild(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	g, err := svc.CreateGuild(ctx, owner, "My Guild", "desc", "")
	require.NoError(t, err)
	assert.Equal(t, "My Guild", g.Name)
	assert.Equal(t, owner, g.OwnerID)

	// Owner should be auto-added as member
	_, count, err := svc.GetGuild(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// --- GetGuild ---

func TestGetGuild(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	g, err := svc.CreateGuild(ctx, owner, "My Guild", "", "")
	require.NoError(t, err)

	fetched, count, err := svc.GetGuild(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, g.ID, fetched.ID)
	assert.Equal(t, int64(1), count)
}

// --- UpdateGuild ---

func TestUpdateGuildByOwner(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	g, err := svc.CreateGuild(ctx, owner, "Old Name", "", "")
	require.NoError(t, err)

	newName := "New Name"
	updated, err := svc.UpdateGuild(ctx, owner, g.ID, db.UpdateGuildParams{
		Name: &newName,
	})
	require.NoError(t, err)
	assert.Equal(t, "New Name", updated.Name)
}

func TestUpdateGuildByNonOwnerFails(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	other := createTestUser(t, infra, "other", "other@test.com")

	g, err := svc.CreateGuild(ctx, owner, "Guild", "", "")
	require.NoError(t, err)

	newName := "Hacked"
	_, err = svc.UpdateGuild(ctx, other, g.ID, db.UpdateGuildParams{
		Name: &newName,
	})
	assert.ErrorIs(t, err, ErrNotGuildOwner)
}

// --- DeleteGuild ---

func TestDeleteGuildByOwner(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	g, err := svc.CreateGuild(ctx, owner, "Guild", "", "")
	require.NoError(t, err)

	err = svc.DeleteGuild(ctx, owner, g.ID)
	require.NoError(t, err)

	_, _, err = svc.GetGuild(ctx, g.ID)
	assert.ErrorIs(t, err, ErrGuildNotFound)
}

func TestDeleteGuildByNonOwnerFails(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	other := createTestUser(t, infra, "other", "other@test.com")

	g, err := svc.CreateGuild(ctx, owner, "Guild", "", "")
	require.NoError(t, err)

	err = svc.DeleteGuild(ctx, other, g.ID)
	assert.ErrorIs(t, err, ErrNotGuildOwner)
}

// --- JoinGuild via invite ---

func TestJoinGuildViaInvite(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	joiner := createTestUser(t, infra, "joiner", "joiner@test.com")

	g, channelID := createGuildWithChannel(t, svc, infra, owner)

	invite, err := svc.CreateInvite(ctx, owner, g.ID, channelID, 0, 0)
	require.NoError(t, err)

	joined, err := svc.JoinGuild(ctx, joiner, invite.Code)
	require.NoError(t, err)
	assert.Equal(t, g.ID, joined.Guild.ID)

	_, count, err := svc.GetGuild(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

// --- LeaveGuild ---

func TestLeaveGuild(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	member := createTestUser(t, infra, "member", "member@test.com")

	g, channelID := createGuildWithChannel(t, svc, infra, owner)

	invite, err := svc.CreateInvite(ctx, owner, g.ID, channelID, 0, 0)
	require.NoError(t, err)
	_, err = svc.JoinGuild(ctx, member, invite.Code)
	require.NoError(t, err)

	err = svc.LeaveGuild(ctx, member, g.ID)
	require.NoError(t, err)

	_, count, err := svc.GetGuild(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestOwnerCannotLeave(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	g, err := svc.CreateGuild(ctx, owner, "Guild", "", "")
	require.NoError(t, err)

	err = svc.LeaveGuild(ctx, owner, g.ID)
	assert.ErrorIs(t, err, ErrOwnerCannotLeave)
}

// --- KickMember ---

func TestKickMember(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	member := createTestUser(t, infra, "member", "member@test.com")

	g, channelID := createGuildWithChannel(t, svc, infra, owner)

	invite, err := svc.CreateInvite(ctx, owner, g.ID, channelID, 0, 0)
	require.NoError(t, err)
	_, err = svc.JoinGuild(ctx, member, invite.Code)
	require.NoError(t, err)

	err = svc.KickMember(ctx, owner, g.ID, member)
	require.NoError(t, err)

	_, count, err := svc.GetGuild(ctx, g.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

// --- BanMember ---

func TestBanMember(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	member := createTestUser(t, infra, "member", "member@test.com")

	g, channelID := createGuildWithChannel(t, svc, infra, owner)

	invite, err := svc.CreateInvite(ctx, owner, g.ID, channelID, 0, 0)
	require.NoError(t, err)
	_, err = svc.JoinGuild(ctx, member, invite.Code)
	require.NoError(t, err)

	err = svc.BanMember(ctx, owner, g.ID, member, "bad behavior")
	require.NoError(t, err)

	// Banned user should not be able to rejoin
	invite2, err := svc.CreateInvite(ctx, owner, g.ID, channelID, 0, 0)
	require.NoError(t, err)
	_, err = svc.JoinGuild(ctx, member, invite2.Code)
	assert.ErrorIs(t, err, ErrUserBanned)
}

// --- CreateRole / AssignRole / RemoveRole / DeleteRole ---

func TestRoleLifecycle(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	member := createTestUser(t, infra, "member", "member@test.com")

	g, channelID := createGuildWithChannel(t, svc, infra, owner)

	// Add member to guild
	invite, err := svc.CreateInvite(ctx, owner, g.ID, channelID, 0, 0)
	require.NoError(t, err)
	_, err = svc.JoinGuild(ctx, member, invite.Code)
	require.NoError(t, err)

	// Create role
	role, err := svc.CreateRole(ctx, owner, g.ID, "Moderator", "#FF0000")
	require.NoError(t, err)
	assert.Equal(t, "Moderator", role.Name)
	assert.Equal(t, "#FF0000", role.Color)

	// Assign role to member
	err = svc.AssignRole(ctx, owner, g.ID, member, role.ID)
	require.NoError(t, err)

	// Remove role from member
	err = svc.RemoveRole(ctx, owner, g.ID, member, role.ID)
	require.NoError(t, err)

	// Delete role
	err = svc.DeleteRole(ctx, owner, g.ID, role.ID)
	require.NoError(t, err)
}

func TestCreateRoleNonOwnerFails(t *testing.T) {
	svc, infra := setupGuildService(t)
	ctx := context.Background()

	owner := createTestUser(t, infra, "owner", "owner@test.com")
	other := createTestUser(t, infra, "other", "other@test.com")

	g, err := svc.CreateGuild(ctx, owner, "Guild", "", "")
	require.NoError(t, err)

	_, err = svc.CreateRole(ctx, other, g.ID, "Admin", "#000000")
	assert.ErrorIs(t, err, ErrNotGuildOwner)
}
