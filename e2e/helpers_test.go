package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
)

const serverAddr = "localhost:50051"

// testUser holds user credentials and tokens
type testUser struct {
	ID           string
	Username     string // base name passed to Register
	Handle       string // full "name#1234" handle assigned by the server
	Email        string
	AccessToken  string
	RefreshToken string
}

// resolveHandle fetches the server-assigned "name#1234" handle for a freshly
// registered user. Required because SendFriendRequest now keys on the handle.
func resolveHandle(t *testing.T, userClient userv1.UserServiceClient, u *testUser) {
	t.Helper()
	resp, err := userClient.GetUser(u.ctx(), &userv1.GetUserRequest{UserId: u.ID})
	require.NoError(t, err)
	u.Handle = resp.GetUser().GetUsername()
}

func (u *testUser) ctx() context.Context {
	md := metadata.New(map[string]string{"authorization": "Bearer " + u.AccessToken})
	return metadata.NewOutgoingContext(context.Background(), md)
}

func authCtxFromToken(token string) context.Context {
	md := metadata.New(map[string]string{"authorization": "Bearer " + token})
	return metadata.NewOutgoingContext(context.Background(), md)
}

func strPtr(s string) *string { return &s }

// registerAndVerify registers a user, reads OTP from Mailpit, verifies, returns user with tokens.
func registerAndVerify(t *testing.T, authClient authv1.AuthServiceClient, username, email, password string) *testUser {
	t.Helper()
	ctx := context.Background()

	regResp, err := authClient.Register(ctx, &authv1.RegisterRequest{
		Username: username, Email: email, Password: password,
	})
	require.NoError(t, err, "failed to register %s", username)

	time.Sleep(500 * time.Millisecond)
	otp := readOTPFromMailpit(t, email)

	verifyResp, err := authClient.VerifyOTP(ctx, &authv1.VerifyOTPRequest{
		Email: email, Otp: otp,
	})
	require.NoError(t, err, "failed to verify OTP for %s", username)

	return &testUser{
		ID: regResp.UserId, Username: username, Email: email,
		AccessToken: verifyResp.AccessToken, RefreshToken: verifyResp.RefreshToken,
	}
}

// readOTPFromMailpit reads the latest email to an address from Mailpit and extracts 6-digit OTP.
func readOTPFromMailpit(t *testing.T, email string) string {
	t.Helper()
	url := fmt.Sprintf("http://localhost:8025/api/v1/search?query=to:%s&limit=1", email)
	resp, err := http.Get(url)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var result struct {
		Messages []struct {
			ID string `json:"ID"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &result), "mailpit parse: %s", string(body))
	require.NotEmpty(t, result.Messages, "no emails for %s", email)

	msgResp, err := http.Get(fmt.Sprintf("http://localhost:8025/api/v1/message/%s", result.Messages[0].ID))
	require.NoError(t, err)
	defer msgResp.Body.Close()
	msgBody, _ := io.ReadAll(msgResp.Body)

	var msg struct{ Text string }
	json.Unmarshal(msgBody, &msg)

	re := regexp.MustCompile(`\b(\d{6})\b`)
	matches := re.FindStringSubmatch(msg.Text)
	require.NotEmpty(t, matches, "no OTP in email: %s", msg.Text)
	return matches[1]
}

// newConn creates a gRPC connection to the test server.
func newConn(t *testing.T) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	return conn
}
