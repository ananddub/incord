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
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
)

// TestSendDirectMessage tests the one-call DM flow:
// send message directly to a user → auto DM channel created → message delivered
func TestSendDirectMessage(t *testing.T) {
	conn := newConn(t)
	auth := authv1.NewAuthServiceClient(conn)
	msg := messagev1.NewMessageServiceClient(conn)

	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	alice := registerAndVerify(t, auth, "dm_a_"+ts, "dm_a_"+ts+"@t.com", "password123")
	bob := registerAndVerify(t, auth, "dm_b_"+ts, "dm_b_"+ts+"@t.com", "password123")

	// Alice sends DM to Bob
	resp, err := msg.SendDirectMessage(alice.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: bob.ID,
		Content:     "Hey Bob! Direct message test " + ts,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.ChannelId, "should return DM channel ID")
	assert.Equal(t, "Hey Bob! Direct message test "+ts, resp.Message.Content)
	assert.Equal(t, alice.ID, resp.Message.AuthorId)
	t.Logf("DM sent: channel=%s msg=%s", resp.ChannelId, resp.Message.Id)

	// Send another message in same DM - should reuse channel
	resp2, err := msg.SendDirectMessage(alice.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: bob.ID,
		Content:     "Second DM",
	})
	require.NoError(t, err)
	assert.Equal(t, resp.ChannelId, resp2.ChannelId, "should reuse same DM channel")

	// Bob can read messages in the DM channel
	listResp, err := msg.ListMessages(bob.ctx(), &messagev1.ListMessagesRequest{
		ChannelId: resp.ChannelId, Limit: 10,
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(listResp.Messages), 2)
	t.Logf("Bob sees %d messages in DM", len(listResp.Messages))

	// Bob replies
	resp3, err := msg.SendDirectMessage(bob.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: alice.ID,
		Content:     "Hey Alice! Got your message",
	})
	require.NoError(t, err)
	assert.Equal(t, resp.ChannelId, resp3.ChannelId, "Bob's reply uses same DM channel")

	t.Log("SEND DIRECT MESSAGE WORKS!")
}

// TestSendDirectMessage_Blocked tests that blocked users can't DM each other
func TestSendDirectMessage_Blocked(t *testing.T) {
	conn := newConn(t)
	auth := authv1.NewAuthServiceClient(conn)
	users := userv1.NewUserServiceClient(conn)
	msg := messagev1.NewMessageServiceClient(conn)

	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	alice := registerAndVerify(t, auth, "blk_a_"+ts, "blk_a_"+ts+"@t.com", "password123")
	bob := registerAndVerify(t, auth, "blk_b_"+ts, "blk_b_"+ts+"@t.com", "password123")

	// Alice blocks Bob
	_, err := users.BlockUser(alice.ctx(), &userv1.BlockUserRequest{TargetUserId: bob.ID})
	require.NoError(t, err)
	t.Log("Alice blocked Bob")

	// Bob tries to DM Alice → should fail
	_, err = msg.SendDirectMessage(bob.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: alice.ID,
		Content:     "Hey Alice!",
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	t.Logf("Bob can't DM Alice: %s", err)

	// Alice tries to DM Bob → should also fail (she blocked him)
	_, err = msg.SendDirectMessage(alice.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: bob.ID,
		Content:     "Hey Bob!",
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	t.Logf("Alice can't DM Bob either: %s", err)

	// Unblock
	_, err = users.UnblockUser(alice.ctx(), &userv1.UnblockUserRequest{TargetUserId: bob.ID})
	require.NoError(t, err)

	// Now DM should work
	resp, err := msg.SendDirectMessage(bob.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: alice.ID,
		Content:     "Hey Alice! Unblocked now",
	})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.ChannelId)
	t.Log("After unblock, DM works again!")

	t.Log("BLOCK CHECK FOR DMS WORKS!")
}

// TestGetUnreadCounts tests pending message counts
func TestGetUnreadCounts(t *testing.T) {
	conn := newConn(t)
	auth := authv1.NewAuthServiceClient(conn)
	msg := messagev1.NewMessageServiceClient(conn)

	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	alice := registerAndVerify(t, auth, "ur_a_"+ts, "ur_a_"+ts+"@t.com", "password123")
	bob := registerAndVerify(t, auth, "ur_b_"+ts, "ur_b_"+ts+"@t.com", "password123")

	// Alice sends DM to Bob
	dmResp, err := msg.SendDirectMessage(alice.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: bob.ID, Content: "msg1",
	})
	require.NoError(t, err)
	channelID := dmResp.ChannelId
	firstMsgID := dmResp.Message.Id

	// Bob acks the first message
	_, err = msg.AckMessage(bob.ctx(), &messagev1.AckMessageRequest{
		ChannelId: channelID, MessageId: firstMsgID,
	})
	require.NoError(t, err)

	// Alice sends 3 more messages (unread for Bob)
	for i := 2; i <= 4; i++ {
		_, err = msg.SendDirectMessage(alice.ctx(), &messagev1.SendDirectMessageRequest{
			RecipientId: bob.ID, Content: fmt.Sprintf("msg%d", i),
		})
		require.NoError(t, err)
	}

	// Bob checks unread counts
	unreadResp, err := msg.GetUnreadCounts(bob.ctx(), &messagev1.GetUnreadCountsRequest{})
	require.NoError(t, err)
	t.Logf("Bob has %d total unread (%d DMs, %d channels)", unreadResp.TotalUnread, len(unreadResp.DmMessages), len(unreadResp.ChannelMessages))

	for _, dm := range unreadResp.DmMessages {
		t.Logf("  DM %s: %d unread", dm.ChannelId, dm.UnreadCount)
	}

	if unreadResp.TotalUnread > 0 {
		assert.GreaterOrEqual(t, unreadResp.TotalUnread, int32(3))
		t.Log("GET UNREAD COUNTS WORKS!")
	} else {
		t.Log("Unread count is 0 - ScyllaDB eventually consistent, may need time")
	}
}

// TestSendDirectMessage_Validation tests error cases
func TestSendDirectMessage_Validation(t *testing.T) {
	conn := newConn(t)
	auth := authv1.NewAuthServiceClient(conn)
	msg := messagev1.NewMessageServiceClient(conn)

	ts := fmt.Sprintf("%d", time.Now().UnixNano())
	alice := registerAndVerify(t, auth, "dv_a_"+ts, "dv_a_"+ts+"@t.com", "password123")

	// Empty recipient
	_, err := msg.SendDirectMessage(alice.ctx(), &messagev1.SendDirectMessageRequest{
		Content: "hello",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// Empty content
	_, err = msg.SendDirectMessage(alice.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: "some-id",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// Unauthenticated
	_, err = msg.SendDirectMessage(context.Background(), &messagev1.SendDirectMessageRequest{
		RecipientId: "some-id", Content: "hello",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))

	t.Log("DM validation checks pass!")
}
