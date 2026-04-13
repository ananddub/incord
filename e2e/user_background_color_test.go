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

func TestUserBackgroundColor(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	userClient := userv1.NewUserServiceClient(conn)

	ts := time.Now().UnixNano()
	me := registerAndVerify(t, authClient,
		fmt.Sprintf("bguser%d", ts),
		fmt.Sprintf("bguser%d@test.local", ts),
		"testpass123")
	ctx := me.ctx()

	// Fresh user should have empty background_color.
	getResp, err := userClient.GetUser(ctx, &userv1.GetUserRequest{UserId: me.ID})
	require.NoError(t, err)
	assert.Empty(t, getResp.User.BackgroundColor)

	// Update with a valid hex color.
	color := "#5865F2"
	updResp, err := userClient.UpdateUser(ctx, &userv1.UpdateUserRequest{
		BackgroundColor: &color,
	})
	require.NoError(t, err)
	assert.Equal(t, color, updResp.User.BackgroundColor)

	// GetUser should also return it.
	getResp2, err := userClient.GetUser(ctx, &userv1.GetUserRequest{UserId: me.ID})
	require.NoError(t, err)
	assert.Equal(t, color, getResp2.User.BackgroundColor)

	// Short form #RGB should work.
	short := "#F0A"
	updResp2, err := userClient.UpdateUser(ctx, &userv1.UpdateUserRequest{
		BackgroundColor: &short,
	})
	require.NoError(t, err)
	assert.Equal(t, short, updResp2.User.BackgroundColor)

	// Invalid color rejected by protovalidate.
	bad := "notacolor"
	_, err = userClient.UpdateUser(ctx, &userv1.UpdateUserRequest{
		BackgroundColor: &bad,
	})
	require.Error(t, err, "non-hex color should be rejected")
}
