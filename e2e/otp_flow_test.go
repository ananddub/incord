package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
)

// TestOTPRegistrationFlow tests:
// 1. Register → gets "OTP sent" message (no tokens)
// 2. Login before verify → fails with "not verified"
// 3. Read OTP from Mailpit inbox
// 4. VerifyOTP → gets tokens
// 5. Login now works
func TestOTPRegistrationFlow(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	ctx := context.Background()
	ts := fmt.Sprintf("%d", time.Now().UnixNano())

	email := "otp_test_" + ts + "@test.com"
	password := "securepassword123"

	// ── Step 1: Register ──
	regResp, err := authClient.Register(ctx, &authv1.RegisterRequest{
		Username: "otp_user_" + ts,
		Email:    email,
		Password: password,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, regResp.UserId)
	assert.Contains(t, regResp.Message, "OTP sent")
	t.Logf("Registered: %s - %s", regResp.UserId, regResp.Message)

	// ── Step 2: Login before verify → should fail ──
	_, err = authClient.Login(ctx, &authv1.LoginRequest{
		Email: email, Password: password,
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	t.Log("Login before verify: correctly rejected")

	// ── Step 3: Read OTP from Mailpit ──
	time.Sleep(1 * time.Second) // wait for email delivery
	otp := readOTPFromMailpit(t, email)
	t.Logf("OTP from Mailpit: %s", otp)

	// ── Step 4: Verify OTP → get tokens ──
	verifyResp, err := authClient.VerifyOTP(ctx, &authv1.VerifyOTPRequest{
		Email: email, Otp: otp,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, verifyResp.UserId)
	assert.NotEmpty(t, verifyResp.AccessToken)
	assert.NotEmpty(t, verifyResp.RefreshToken)
	t.Logf("OTP verified! Got tokens for user %s", verifyResp.UserId)

	// ── Step 5: Login now works ──
	loginResp, err := authClient.Login(ctx, &authv1.LoginRequest{
		Email: email, Password: password,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, loginResp.AccessToken)
	assert.Equal(t, regResp.UserId, loginResp.UserId)
	t.Log("Login after verify: success!")

	// ── Step 6: Wrong OTP should fail ──
	_, err = authClient.VerifyOTP(ctx, &authv1.VerifyOTPRequest{
		Email: email, Otp: "000000",
	})
	require.Error(t, err)
	t.Log("Wrong OTP: correctly rejected")

	// ── Step 7: ResendOTP ──
	resendResp, err := authClient.ResendOTP(ctx, &authv1.ResendOTPRequest{
		Email: email,
	})
	require.NoError(t, err)
	assert.Contains(t, resendResp.Message, "OTP sent")
	t.Log("ResendOTP: success")

	t.Log("OTP REGISTRATION FLOW COMPLETE!")
}
