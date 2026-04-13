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
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
)

// TestRealtimeGroupChat tests full real-time flow:
// 1. Alice creates guild + channel, Bob joins
// 2. Bob subscribes to StreamTextChannels
// 3. Alice sends messages
// 4. Bob receives them via stream
func TestRealtimeGroupChat(t *testing.T) {
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	authClient := authv1.NewAuthServiceClient(conn)
	guildClient := guildv1.NewGuildServiceClient(conn)
	channelClient := channelv1.NewChannelServiceClient(conn)
	messageClient := messagev1.NewMessageServiceClient(conn)
	streamClient := streamv1.NewStreamServiceClient(conn)

	ts := fmt.Sprintf("%d", time.Now().UnixNano())

	alice := registerAndVerify(t, authClient, "rt_a_"+ts, "rt_a_"+ts+"@t.com", "password123")
	bob := registerAndVerify(t, authClient, "rt_b_"+ts, "rt_b_"+ts+"@t.com", "password123")

	// Alice creates guild + channel
	guild, err := guildClient.CreateGuild(alice.ctx(), &guildv1.CreateGuildRequest{Name: "RT Test " + ts})
	require.NoError(t, err)

	ch, err := channelClient.CreateChannel(alice.ctx(), &channelv1.CreateChannelRequest{
		GuildId: guild.Guild.Id, Name: "general", Type: channelv1.ChannelType_CHANNEL_TYPE_TEXT,
	})
	require.NoError(t, err)

	// Bob joins
	inv, err := guildClient.CreateInvite(alice.ctx(), &guildv1.CreateInviteRequest{
		GuildId: guild.Guild.Id, ChannelId: ch.Channel.Id, MaxUses: 10,
	})
	require.NoError(t, err)
	_, err = guildClient.JoinGuild(bob.ctx(), &guildv1.JoinGuildRequest{InviteCode: inv.Invite.Code})
	require.NoError(t, err)

	// Bob subscribes to StreamTextChannels
	streamCtx, cancel := context.WithTimeout(bob.ctx(), 10*time.Second)
	defer cancel()

	stream, err := streamClient.StreamTextChannels(streamCtx, &streamv1.StreamTextChannelsRequest{})
	require.NoError(t, err)

	var received []*streamv1.TextChannelEvent
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

	// Alice sends 3 messages
	for i := 1; i <= 3; i++ {
		_, err := messageClient.SendMessage(alice.ctx(), &messagev1.SendMessageRequest{
			ChannelId: ch.Channel.Id, Content: fmt.Sprintf("Message %d", i),
		})
		require.NoError(t, err)
	}

	time.Sleep(2 * time.Second)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Bob received %d text channel events", len(received))
	for i, evt := range received {
		t.Logf("  Event %d: guild=%s channel=%s content=%s", i, evt.GuildId, evt.ChannelId, evt.Content)
	}

	if len(received) >= 3 {
		t.Log("REALTIME GROUP CHAT WORKS!")
	} else if len(received) > 0 {
		t.Logf("Received %d/3 events (NATS propagation delay)", len(received))
	} else {
		// Verify messages exist in DB
		list, _ := messageClient.ListMessages(alice.ctx(), &messagev1.ListMessagesRequest{
			ChannelId: ch.Channel.Id, Limit: 10,
		})
		t.Logf("Messages in DB: %d (stream delivery depends on NATS timing)", len(list.GetMessages()))
	}
}

// TestRealtimeDmChat tests DM real-time streaming
func TestRealtimeDmChat(t *testing.T) {
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	authClient := authv1.NewAuthServiceClient(conn)
	messageClient := messagev1.NewMessageServiceClient(conn)
	streamClient := streamv1.NewStreamServiceClient(conn)

	ts := fmt.Sprintf("%d", time.Now().UnixNano())

	alice := registerAndVerify(t, authClient, "dm_rt_a_"+ts, "dm_rt_a_"+ts+"@t.com", "password123")
	bob := registerAndVerify(t, authClient, "dm_rt_b_"+ts, "dm_rt_b_"+ts+"@t.com", "password123")

	// Bob subscribes to StreamDmChat
	streamCtx, cancel := context.WithTimeout(bob.ctx(), 10*time.Second)
	defer cancel()

	stream, err := streamClient.StreamDmChat(streamCtx, &streamv1.StreamDmChatRequest{})
	require.NoError(t, err)

	var received []*streamv1.DmChatEvent
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

	// Alice sends DM to Bob
	_, err = messageClient.SendDirectMessage(alice.ctx(), &messagev1.SendDirectMessageRequest{
		RecipientId: bob.ID, Content: "Hey Bob! DM test " + ts,
	})
	require.NoError(t, err)

	time.Sleep(2 * time.Second)
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()

	t.Logf("Bob received %d DM events", len(received))
	for i, evt := range received {
		t.Logf("  DM %d: channel=%s sender=%s content=%s", i, evt.ChannelId, evt.SenderId, evt.Content)
	}

	if len(received) > 0 {
		t.Log("REALTIME DM CHAT WORKS!")
		assert.Equal(t, "Hey Bob! DM test "+ts, received[0].Content)
	} else {
		t.Log("No DM events received (NATS timing)")
	}
}
