package presence

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/testutil"
)

func setupPresence(t *testing.T) (*Service, *testutil.TestInfra) {
	t.Helper()
	infra := testutil.SetupTestInfra(t)
	svc := NewService(infra.Redis, nil)
	return svc, infra
}

func TestGetPresence_Online(t *testing.T) {
	svc, infra := setupPresence(t)
	ctx := context.Background()

	userID := "user-abc-123"
	now := time.Now()

	// Seed Redis directly to avoid calling UpdatePresence (which uses the nil producer).
	err := infra.Redis.HSet(ctx, presenceKeyPrefix+userID, map[string]interface{}{
		"status":        int32(presencev1.Status_STATUS_ONLINE),
		"custom_status": "coding",
		"last_seen":     now.Unix(),
	}).Err()
	require.NoError(t, err)

	p, err := svc.GetPresence(ctx, userID)
	require.NoError(t, err)

	assert.Equal(t, userID, p.UserId)
	assert.Equal(t, presencev1.Status_STATUS_ONLINE, p.Status)
	assert.Equal(t, "coding", p.CustomStatus)
	assert.NotNil(t, p.LastSeen)
	assert.Equal(t, now.Unix(), p.LastSeen.AsTime().Unix())
}

func TestGetPresence_NotFound_ReturnsOffline(t *testing.T) {
	svc, _ := setupPresence(t)
	ctx := context.Background()

	p, err := svc.GetPresence(ctx, "nonexistent-user")
	require.NoError(t, err)

	assert.Equal(t, "nonexistent-user", p.UserId)
	assert.Equal(t, presencev1.Status_STATUS_OFFLINE, p.Status)
	assert.Empty(t, p.CustomStatus)
}

func TestGetBulkPresence(t *testing.T) {
	svc, infra := setupPresence(t)
	ctx := context.Background()

	// Seed two users, leave a third missing.
	err := infra.Redis.HSet(ctx, presenceKeyPrefix+"user-1", map[string]interface{}{
		"status":        int32(presencev1.Status_STATUS_ONLINE),
		"custom_status": "hello",
		"last_seen":     time.Now().Unix(),
	}).Err()
	require.NoError(t, err)

	err = infra.Redis.HSet(ctx, presenceKeyPrefix+"user-2", map[string]interface{}{
		"status":        int32(presencev1.Status_STATUS_DND),
		"custom_status": "",
		"last_seen":     time.Now().Unix(),
	}).Err()
	require.NoError(t, err)

	presences, err := svc.GetBulkPresence(ctx, []string{"user-1", "user-2", "user-3"})
	require.NoError(t, err)
	require.Len(t, presences, 3)

	assert.Equal(t, presencev1.Status_STATUS_ONLINE, presences[0].Status)
	assert.Equal(t, "hello", presences[0].CustomStatus)

	assert.Equal(t, presencev1.Status_STATUS_DND, presences[1].Status)

	// user-3 was never set — should be offline.
	assert.Equal(t, presencev1.Status_STATUS_OFFLINE, presences[2].Status)
}

func TestGetBulkPresence_Empty(t *testing.T) {
	svc, _ := setupPresence(t)
	ctx := context.Background()

	presences, err := svc.GetBulkPresence(ctx, []string{})
	require.NoError(t, err)
	assert.Empty(t, presences)
}
