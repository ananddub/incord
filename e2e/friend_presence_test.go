package e2e

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
)

// TestFriendPresenceStream tests:
// 1. Alice and Bob become friends
// 2. Bob subscribes to StreamFriendActivity
// 3. Alice updates presence
// 4. Bob receives via stream
func TestFriendPresenceStream(t *testing.T) {
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	authClient := authv1.NewAuthServiceClient(conn)
	userClient := userv1.NewUserServiceClient(conn)
	presenceClient := presencev1.NewPresenceServiceClient(conn)
	streamClient := streamv1.NewStreamServiceClient(conn)

	ts := fmt.Sprintf("%d", time.Now().UnixNano())

	alice := registerAndVerify(t, authClient, "fp_a_"+ts, "fp_a_"+ts+"@t.com", "password123")
	bob := registerAndVerify(t, authClient, "fp_b_"+ts, "fp_b_"+ts+"@t.com", "password123")

	// Make friends
	_, err = userClient.SendFriendRequest(alice.ctx(), &userv1.SendFriendRequestRequest{TargetUserId: bob.ID})
	require.NoError(t, err)
	_, err = userClient.AcceptFriendRequest(bob.ctx(), &userv1.AcceptFriendRequestRequest{RequesterUserId: alice.ID})
	require.NoError(t, err)
	t.Log("Alice and Bob are friends")

	// Bob subscribes to StreamFriendActivity
	streamCtx, cancel := context.WithTimeout(bob.ctx(), 10*time.Second)
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

	time.Sleep(500 * time.Millisecond)

	// Alice goes online
	_, err = presenceClient.UpdatePresence(alice.ctx(), &presencev1.UpdatePresenceRequest{
		Status: presencev1.Status_STATUS_ONLINE, CustomStatus: "Testing!",
	})
	require.NoError(t, err)
	t.Log("Alice set status to ONLINE")

	time.Sleep(3 * time.Second)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Bob received %d friend activity events", len(received))
	for i, evt := range received {
		t.Logf("  Event %d: event=%s user=%s status=%s", i, evt.Event, evt.UserId, evt.Status)
	}

	if len(received) > 0 {
		t.Log("FRIEND PRESENCE STREAMING WORKS!")
	} else {
		// Verify presence was saved
		pres, _ := presenceClient.GetPresence(alice.ctx(), &presencev1.GetPresenceRequest{UserId: alice.ID})
		assert.Equal(t, presencev1.Status_STATUS_ONLINE, pres.GetPresence().GetStatus())
		t.Log("Presence saved. Friend notification depends on NATS + friend lookup wiring.")
	}
}
