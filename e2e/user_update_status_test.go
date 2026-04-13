package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
)

func TestUserUpdateStatus(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	userClient := userv1.NewUserServiceClient(conn)

	ts := time.Now().UnixNano()
	me := registerAndVerify(t, authClient,
		fmt.Sprintf("statususer%d", ts),
		fmt.Sprintf("statususer%d@test.local", ts),
		"testpass123")
	ctx := me.ctx()

	// Dedicated UpdateStatus call.
	updResp, err := userClient.UpdateStatus(ctx, &userv1.UpdateStatusRequest{
		Status: "Building stuff 🚀",
	})
	require.NoError(t, err)
	assert.Equal(t, "Building stuff 🚀", updResp.User.Status)

	// GetUser reflects the new status.
	getResp2, err := userClient.GetUser(ctx, &userv1.GetUserRequest{UserId: me.ID})
	require.NoError(t, err)
	assert.Equal(t, "Building stuff 🚀", getResp2.User.Status)

	// Clearing the status (empty string).
	clrResp, err := userClient.UpdateStatus(ctx, &userv1.UpdateStatusRequest{Status: ""})
	require.NoError(t, err)
	assert.Empty(t, clrResp.User.Status)

	// Too-long status rejected by protovalidate (max_len 128).
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'a'
	}
	_, err = userClient.UpdateStatus(ctx, &userv1.UpdateStatusRequest{Status: string(long)})
	require.Error(t, err, "over 128 chars should be rejected")
}
