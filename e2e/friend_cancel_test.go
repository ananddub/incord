package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
)

// TestCancelFriendRequest verifies the sender can withdraw their own
// pending outgoing request before the recipient has acted on it.
func TestCancelFriendRequest(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	userClient := userv1.NewUserServiceClient(conn)

	ts := time.Now().UnixNano()
	alice := registerAndVerify(t, authClient,
		fmt.Sprintf("cancel_a_%d", ts),
		fmt.Sprintf("cancel_a_%d@t.com", ts),
		"password123")
	bob := registerAndVerify(t, authClient,
		fmt.Sprintf("cancel_b_%d", ts),
		fmt.Sprintf("cancel_b_%d@t.com", ts),
		"password123")

	// Bob subscribes to his friend activity stream BEFORE the flow so the
	// cancel event is captured end-to-end via NATS.
	streamClient := streamv1.NewStreamServiceClient(conn)
	streamCtx, cancelStream := context.WithTimeout(bob.ctx(), 15*time.Second)
	defer cancelStream()
	stream, err := streamClient.StreamFriendActivity(streamCtx, &streamv1.StreamFriendActivityRequest{})
	require.NoError(t, err)

	var received []*streamv1.FriendActivityEvent
	var mu sync.Mutex
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			evt, err := stream.Recv()
			if err != nil {
				return
			}
			mu.Lock()
			received = append(received, evt)
			mu.Unlock()
		}
	}()
	time.Sleep(300 * time.Millisecond)

	// Alice sends a request to Bob.
	_, err = userClient.SendFriendRequest(alice.ctx(), &userv1.SendFriendRequestRequest{
		TargetUserId: bob.ID,
	})
	require.NoError(t, err)

	// Alice sees it in her outgoing list.
	pendA, err := userClient.ListPendingRequests(alice.ctx(), &userv1.ListPendingRequestsRequest{})
	require.NoError(t, err)
	require.Len(t, pendA.Outgoing, 1)
	assert.Equal(t, bob.ID, pendA.Outgoing[0].FriendId)

	// Bob sees it in his incoming list.
	pendB, err := userClient.ListPendingRequests(bob.ctx(), &userv1.ListPendingRequestsRequest{})
	require.NoError(t, err)
	require.Len(t, pendB.Incoming, 1)

	// Alice cancels her own request.
	_, err = userClient.CancelFriendRequest(alice.ctx(), &userv1.CancelFriendRequestRequest{
		TargetUserId: bob.ID,
	})
	require.NoError(t, err)

	// Both lists should now be empty.
	pendA, err = userClient.ListPendingRequests(alice.ctx(), &userv1.ListPendingRequestsRequest{})
	require.NoError(t, err)
	assert.Empty(t, pendA.Outgoing, "alice's outgoing should be empty after cancel")

	pendB, err = userClient.ListPendingRequests(bob.ctx(), &userv1.ListPendingRequestsRequest{})
	require.NoError(t, err)
	assert.Empty(t, pendB.Incoming, "bob's incoming should be empty after cancel")

	// Cancelling again must return NotFound.
	_, err = userClient.CancelFriendRequest(alice.ctx(), &userv1.CancelFriendRequestRequest{
		TargetUserId: bob.ID,
	})
	require.Error(t, err, "second cancel should fail")

	// Bob cannot cancel a request alice sent to him (wrong direction).
	_, err = userClient.SendFriendRequest(alice.ctx(), &userv1.SendFriendRequestRequest{
		TargetUserId: bob.ID,
	})
	require.NoError(t, err)
	_, err = userClient.CancelFriendRequest(bob.ctx(), &userv1.CancelFriendRequestRequest{
		TargetUserId: alice.ID,
	})
	require.Error(t, err, "only the sender may cancel their own request")

	// Give the stream a moment to drain, then verify Bob received both
	// friend_request and friend_request_cancelled via NATS.
	time.Sleep(500 * time.Millisecond)
	cancelStream()
	<-done

	mu.Lock()
	defer mu.Unlock()

	var sawRequest, sawCancel bool
	for _, e := range received {
		t.Logf("bob event: %s user=%s username=%q", e.Event, e.UserId, e.Username)
		if e.Event == "friend_request" && e.UserId == alice.ID {
			sawRequest = true
		}
		if e.Event == "friend_request_cancelled" && e.UserId == alice.ID {
			sawCancel = true
			assert.NotEmpty(t, e.Username, "cancel event should include sender username")
		}
	}
	assert.True(t, sawRequest, "bob should have received friend_request via NATS")
	assert.True(t, sawCancel, "bob should have received friend_request_cancelled via NATS")
}
