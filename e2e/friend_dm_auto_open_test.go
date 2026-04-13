package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
)

// TestFriendAcceptOpensDM verifies that accepting a friend request
// automatically creates (or reuses) a DM channel between the two users.
func TestFriendAcceptOpensDM(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	userClient := userv1.NewUserServiceClient(conn)
	channelClient := channelv1.NewChannelServiceClient(conn)

	ts := time.Now().UnixNano()
	alice := registerAndVerify(t, authClient,
		fmt.Sprintf("dmauto_a_%d", ts),
		fmt.Sprintf("dmauto_a_%d@t.com", ts),
		"password123")
	bob := registerAndVerify(t, authClient,
		fmt.Sprintf("dmauto_b_%d", ts),
		fmt.Sprintf("dmauto_b_%d@t.com", ts),
		"password123")

	// Baseline: neither user should have any DM channels yet.
	aliceList, err := channelClient.ListDMChannels(alice.ctx(), &channelv1.ListDMChannelsRequest{})
	require.NoError(t, err)
	require.Empty(t, aliceList.Channels, "alice should start with 0 DM channels")

	bobList, err := channelClient.ListDMChannels(bob.ctx(), &channelv1.ListDMChannelsRequest{})
	require.NoError(t, err)
	require.Empty(t, bobList.Channels, "bob should start with 0 DM channels")

	// Alice sends request → Bob accepts → DM should auto-open.
	_, err = userClient.SendFriendRequest(alice.ctx(), &userv1.SendFriendRequestRequest{
		TargetUserId: bob.ID,
	})
	require.NoError(t, err)

	_, err = userClient.AcceptFriendRequest(bob.ctx(), &userv1.AcceptFriendRequestRequest{
		RequesterUserId: alice.ID,
	})
	require.NoError(t, err)

	// Both users should now see exactly one DM channel — the same one.
	aliceList, err = channelClient.ListDMChannels(alice.ctx(), &channelv1.ListDMChannelsRequest{})
	require.NoError(t, err)
	require.Len(t, aliceList.Channels, 1, "alice should see the auto-opened DM")

	bobList, err = channelClient.ListDMChannels(bob.ctx(), &channelv1.ListDMChannelsRequest{})
	require.NoError(t, err)
	require.Len(t, bobList.Channels, 1, "bob should see the auto-opened DM")

	assert.Equal(t, aliceList.Channels[0].Id, bobList.Channels[0].Id,
		"both users must see the same DM channel")
	assert.Equal(t, channelv1.ChannelType_CHANNEL_TYPE_DM, aliceList.Channels[0].Type)

	// Idempotency: if they somehow friend again (e.g. after remove+add) the
	// DM count must not grow — GetOrCreateDMChannel returns the existing row.
	_, err = userClient.RemoveFriend(alice.ctx(), &userv1.RemoveFriendRequest{FriendId: bob.ID})
	require.NoError(t, err)

	_, err = userClient.SendFriendRequest(alice.ctx(), &userv1.SendFriendRequestRequest{
		TargetUserId: bob.ID,
	})
	require.NoError(t, err)
	_, err = userClient.AcceptFriendRequest(bob.ctx(), &userv1.AcceptFriendRequestRequest{
		RequesterUserId: alice.ID,
	})
	require.NoError(t, err)

	aliceList, err = channelClient.ListDMChannels(alice.ctx(), &channelv1.ListDMChannelsRequest{})
	require.NoError(t, err)
	assert.Len(t, aliceList.Channels, 1, "re-friending must not create a duplicate DM")
}
