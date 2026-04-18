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
	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
)

// TestFriendEventEnriched verifies that friend events include the actor's
// username (and avatar where available), not just the bare UUID.
func TestFriendEventEnriched(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	userClient := userv1.NewUserServiceClient(conn)
	presenceClient := presencev1.NewPresenceServiceClient(conn)
	streamClient := streamv1.NewStreamServiceClient(conn)

	ts := time.Now().UnixNano()
	aliceUsername := fmt.Sprintf("fre_alice_%d", ts)
	bobUsername := fmt.Sprintf("fre_bob_%d", ts)

	alice := registerAndVerify(t, authClient, aliceUsername, aliceUsername+"@t.com", "password123")
	bob := registerAndVerify(t, authClient, bobUsername, bobUsername+"@t.com", "password123")
	resolveHandle(t, userClient, bob)

	// Bob subscribes to friend activity FIRST so he catches the friend_request.
	streamCtx, cancel := context.WithTimeout(bob.ctx(), 15*time.Second)
	defer cancel()
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

	// Alice sends a friend request → Bob should see it with Alice's username.
	_, err = userClient.SendFriendRequest(alice.ctx(), &userv1.SendFriendRequestRequest{
		TargetUsername: bob.Handle,
	})
	require.NoError(t, err)

	// Bob accepts → Alice would see (different stream); we focus on Bob's view.
	_, err = userClient.AcceptFriendRequest(bob.ctx(), &userv1.AcceptFriendRequestRequest{
		RequesterUserId: alice.ID,
	})
	require.NoError(t, err)

	// Now Alice updates presence → Bob (a friend) should see it via Alice's subject.
	_, err = presenceClient.UpdatePresence(alice.ctx(), &presencev1.UpdatePresenceRequest{
		Status:       presencev1.Status_STATUS_ONLINE,
		CustomStatus: "Hello world",
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Bob received %d events", len(received))
	for i, e := range received {
		t.Logf("  [%d] event=%s user_id=%s username=%q status=%s avatar=%q",
			i, e.Event, e.UserId, e.Username, e.Status, e.AvatarUrl)
	}

	// Find the friend_request event and assert it has Alice's username.
	var friendReq *streamv1.FriendActivityEvent
	for _, e := range received {
		if e.Event == streamv1.FriendEventType_FRIEND_EVENT_REQUEST {
			friendReq = e
			break
		}
	}
	require.NotNil(t, friendReq, "friend_request event missing")
	assert.Equal(t, alice.ID, friendReq.UserId)
	// Stream carries the full handle "<base>#1234"; display_name keeps the base name.
	assert.Regexp(t, `^`+aliceUsername+`#[0-9]{4}$`, friendReq.Username, "friend_request must include sender handle")
	assert.Equal(t, aliceUsername, friendReq.DisplayName, "friend_request must include display_name")

	// Find the presence_update event after the friendship and assert username.
	var presenceEvt *streamv1.FriendActivityEvent
	for _, e := range received {
		if e.Event == streamv1.FriendEventType_FRIEND_EVENT_PRESENCE_UPDATE {
			presenceEvt = e
			break
		}
	}
	if presenceEvt != nil {
		assert.Equal(t, alice.ID, presenceEvt.UserId)
		assert.Regexp(t, `^`+aliceUsername+`#[0-9]{4}$`, presenceEvt.Username, "presence_update must include handle")
		assert.Equal(t, "online", presenceEvt.Status)
		assert.Equal(t, "Hello world", presenceEvt.CustomStatus)
	}
}
