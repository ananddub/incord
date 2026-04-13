package e2e

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
)

func TestUserAvatarUpload(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	userClient := userv1.NewUserServiceClient(conn)

	ts := time.Now().UnixNano()
	me := registerAndVerify(t, authClient,
		fmt.Sprintf("avataruser%d", ts),
		fmt.Sprintf("avataruser%d@test.local", ts),
		"testpass123")
	ctx := me.ctx()

	// Avatar should start empty.
	getResp, err := userClient.GetUser(ctx, &userv1.GetUserRequest{UserId: me.ID})
	require.NoError(t, err)
	require.Empty(t, getResp.User.AvatarUrl, "user starts without avatar")

	pngBytes := tinyPNG()

	upResp, err := userClient.UploadUserAvatar(ctx, &userv1.UploadUserAvatarRequest{
		Filename:    "me.png",
		ContentType: "image/png",
		Data:        pngBytes,
	})
	require.NoError(t, err, "UploadUserAvatar failed")
	require.NotEmpty(t, upResp.AvatarUrl, "response must include avatar_url")
	assert.Equal(t, upResp.AvatarUrl, upResp.User.AvatarUrl)

	// URL should be downloadable and bytes should match.
	httpResp, err := (&http.Client{Timeout: 5 * time.Second}).Get(upResp.AvatarUrl)
	require.NoError(t, err)
	defer httpResp.Body.Close()
	require.Equal(t, http.StatusOK, httpResp.StatusCode, "presigned avatar URL should return 200")
	downloaded, err := io.ReadAll(httpResp.Body)
	require.NoError(t, err)
	assert.Equal(t, pngBytes, downloaded)

	// GetUser should return a freshly-signed URL (not the stored object key).
	getResp2, err := userClient.GetUser(ctx, &userv1.GetUserRequest{UserId: me.ID})
	require.NoError(t, err)
	require.NotEmpty(t, getResp2.User.AvatarUrl)
	assert.Contains(t, getResp2.User.AvatarUrl, "X-Amz-Signature", "should be a presigned URL")

	// Negative: protovalidate rejects non-image content types.
	_, err = userClient.UploadUserAvatar(ctx, &userv1.UploadUserAvatarRequest{
		Filename:    "bad.txt",
		ContentType: "text/plain",
		Data:        []byte("not-an-image"),
	})
	require.Error(t, err, "text/plain should be rejected")
}
