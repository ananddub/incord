package stream

const testSecret = "test-secret"

// // mockResolver returns fake guild and friend IDs for testing.
// type mockResolver struct {
// 	guilds  map[string][]string // userID → guildIDs
// 	friends map[string][]string // userID → friendIDs
// }

// func (m *mockResolver) GetUserGuildIDs(_ context.Context, userID string) ([]string, error) {
// 	return m.guilds[userID], nil
// }

// func (m *mockResolver) GetUserFriendIDs(_ context.Context, userID string) ([]string, error) {
// 	return m.friends[userID], nil
// }

// func makeToken(userID string) string {
// 	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
// 		"sub": userID, "exp": time.Now().Add(time.Hour).Unix(),
// 	})
// 	s, _ := token.SignedString([]byte(testSecret))
// 	return s
// }

// func authCtx(userID string) context.Context {
// 	md := metadata.New(map[string]string{"authorization": "Bearer " + makeToken(userID)})
// 	return metadata.NewOutgoingContext(context.Background(), md)
// }

// func setupStreamServer(t *testing.T, resolver *mockResolver) (streamv1.StreamServiceClient, *realtime.LPubSub) {
// 	t.Helper()

// 	hub, err := realtime.NewLPubSub(config.NATSConfig{URL: "nats://localhost:4222"})
// 	require.NoError(t, err, "NATS not running? Start with: docker compose up nats -d")
// 	t.Cleanup(func() { hub.Close() })

// 	handler := NewHandler(hub, resolver)

// 	lis, err := net.Listen("tcp", "127.0.0.1:0")
// 	require.NoError(t, err)

// 	srv := grpc.NewServer(
// 		grpc.ChainUnaryInterceptor(middleware.AuthInterceptor(testSecret)),
// 		grpc.ChainStreamInterceptor(middleware.StreamAuthInterceptor(testSecret)),
// 	)
// 	streamv1.RegisterStreamServiceServer(srv, handler)
// 	go srv.Serve(lis)
// 	t.Cleanup(func() { srv.Stop() })

// 	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
// 	require.NoError(t, err)
// 	t.Cleanup(func() { conn.Close() })

// 	return streamv1.NewStreamServiceClient(conn), hub
// }

// // === StreamTextChannels ===

// func TestStreamTextChannels(t *testing.T) {
// 	resolver := &mockResolver{guilds: map[string][]string{
// 		"user-1": {"guild-1"},
// 	}}
// 	client, hub := setupStreamServer(t, resolver)

// 	ctx, cancel := context.WithTimeout(authCtx("user-1"), 5*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamTextChannels(ctx, &streamv1.StreamTextChannelsRequest{})
// 	require.NoError(t, err)

// 	// Publish message to guild-1 text channel
// 	time.Sleep(200 * time.Millisecond) // wait for subscribe
// 	_ = hub.Publish(realtime.GuildChannelMessage("guild-1", "channel-abc"), map[string]any{
// 		"type": "create", "channel_id": "channel-abc", "guild_id": "guild-1",
// 		"author_id": "user-2", "content": "Hello from guild!",
// 	})

// 	evt, err := stream.Recv()
// 	require.NoError(t, err)
// 	assert.Equal(t, "channel-abc", evt.ChannelId)
// 	assert.Equal(t, "guild-1", evt.GuildId)
// 	assert.Equal(t, "Hello from guild!", evt.Content)
// }

// func TestStreamTextChannels_MultiGuild(t *testing.T) {
// 	resolver := &mockResolver{guilds: map[string][]string{
// 		"user-1": {"guild-1", "guild-2"},
// 	}}
// 	client, hub := setupStreamServer(t, resolver)

// 	ctx, cancel := context.WithTimeout(authCtx("user-1"), 5*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamTextChannels(ctx, &streamv1.StreamTextChannelsRequest{})
// 	require.NoError(t, err)
// 	time.Sleep(200 * time.Millisecond)

// 	// Message in guild-2
// 	_ = hub.Publish(realtime.GuildChannelMessage("guild-2", "ch-xyz"), map[string]any{
// 		"type": "create", "guild_id": "guild-2", "content": "From guild 2",
// 	})

// 	evt, err := stream.Recv()
// 	require.NoError(t, err)
// 	assert.Equal(t, "guild-2", evt.GuildId)
// 	assert.Equal(t, "From guild 2", evt.Content)
// }

// func TestStreamTextChannels_OtherGuildNotReceived(t *testing.T) {
// 	resolver := &mockResolver{guilds: map[string][]string{
// 		"user-1": {"guild-1"}, // only in guild-1
// 	}}
// 	client, hub := setupStreamServer(t, resolver)

// 	ctx, cancel := context.WithTimeout(authCtx("user-1"), 2*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamTextChannels(ctx, &streamv1.StreamTextChannelsRequest{})
// 	require.NoError(t, err)
// 	time.Sleep(200 * time.Millisecond)

// 	// Message in guild-2 (user NOT in this guild)
// 	_ = hub.Publish(realtime.GuildChannelMessage("guild-2", "ch-other"), map[string]any{
// 		"content": "Should not receive",
// 	})

// 	// Should timeout, not receive anything
// 	done := make(chan bool)
// 	go func() {
// 		_, err := stream.Recv()
// 		if err != nil {
// 			done <- true // expected: context deadline or stream closed
// 		} else {
// 			done <- false
// 		}
// 	}()

// 	select {
// 	case received := <-done:
// 		assert.True(t, received, "should not receive message from other guild")
// 	case <-time.After(1 * time.Second):
// 		// correct: no message received
// 	}
// }

// // === StreamDmChat ===

// func TestStreamDmChat(t *testing.T) {
// 	resolver := &mockResolver{}
// 	client, hub := setupStreamServer(t, resolver)

// 	userID := "bob-123"
// 	ctx, cancel := context.WithTimeout(authCtx(userID), 5*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamDmChat(ctx, &streamv1.StreamDmChatRequest{})
// 	require.NoError(t, err)
// 	time.Sleep(200 * time.Millisecond)

// 	// Alice sends DM to Bob (published to dm.bob-123.message.dm-channel-1)
// 	_ = hub.Publish(realtime.DmMessage(userID, "dm-channel-1"), map[string]any{
// 		"type": "create", "channel_id": "dm-channel-1",
// 		"sender_id": "alice-456", "content": "Hey Bob!",
// 	})

// 	evt, err := stream.Recv()
// 	require.NoError(t, err)
// 	assert.Equal(t, "dm-channel-1", evt.ChannelId)
// 	assert.Equal(t, "alice-456", evt.SenderId)
// 	assert.Equal(t, "Hey Bob!", evt.Content)
// }

// func TestStreamDmChat_MultipleDMs(t *testing.T) {
// 	resolver := &mockResolver{}
// 	client, hub := setupStreamServer(t, resolver)

// 	userID := "bob-123"
// 	ctx, cancel := context.WithTimeout(authCtx(userID), 5*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamDmChat(ctx, &streamv1.StreamDmChatRequest{})
// 	require.NoError(t, err)
// 	time.Sleep(200 * time.Millisecond)

// 	// DM from Alice
// 	_ = hub.Publish(realtime.DmMessage(userID, "dm-alice"), map[string]any{
// 		"sender_id": "alice", "content": "Hi from Alice",
// 	})
// 	// DM from Charlie
// 	_ = hub.Publish(realtime.DmMessage(userID, "dm-charlie"), map[string]any{
// 		"sender_id": "charlie", "content": "Hi from Charlie",
// 	})

// 	evt1, _ := stream.Recv()
// 	evt2, _ := stream.Recv()
// 	contents := []string{evt1.Content, evt2.Content}
// 	assert.Contains(t, contents, "Hi from Alice")
// 	assert.Contains(t, contents, "Hi from Charlie")
// }

// // === StreamGuildEvents ===

// func TestStreamGuildEvents(t *testing.T) {
// 	resolver := &mockResolver{guilds: map[string][]string{
// 		"user-1": {"guild-1"},
// 	}}
// 	client, hub := setupStreamServer(t, resolver)

// 	ctx, cancel := context.WithTimeout(authCtx("user-1"), 5*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamGuildEvents(ctx, &streamv1.StreamGuildEventsRequest{})
// 	require.NoError(t, err)
// 	time.Sleep(200 * time.Millisecond)

// 	_ = hub.Publish(realtime.GuildEvents("guild-1"), map[string]any{
// 		"event": "member_add", "guild_id": "guild-1", "user_id": "new-user",
// 	})

// 	evt, err := stream.Recv()
// 	require.NoError(t, err)
// 	assert.Equal(t, "member_add", evt.Event)
// 	assert.Equal(t, "guild-1", evt.GuildId)
// 	assert.Equal(t, "new-user", evt.UserId)
// }

// // === StreamVoiceState ===

// func TestStreamVoiceState(t *testing.T) {
// 	resolver := &mockResolver{guilds: map[string][]string{
// 		"user-1": {"guild-1"},
// 	}}
// 	client, hub := setupStreamServer(t, resolver)

// 	ctx, cancel := context.WithTimeout(authCtx("user-1"), 5*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamVoiceState(ctx, &streamv1.StreamVoiceStateRequest{})
// 	require.NoError(t, err)
// 	time.Sleep(200 * time.Millisecond)

// 	_ = hub.Publish(realtime.GuildChannelVoice("guild-1", "vc-1"), map[string]any{
// 		"event": "join", "channel_id": "vc-1", "user_id": "user-2",
// 	})

// 	evt, err := stream.Recv()
// 	require.NoError(t, err)
// 	assert.Equal(t, "join", evt.Event)
// 	assert.Equal(t, "vc-1", evt.ChannelId)
// 	assert.Equal(t, "user-2", evt.UserId)
// }

// // === StreamTyping ===

// func TestStreamTyping(t *testing.T) {
// 	resolver := &mockResolver{guilds: map[string][]string{
// 		"user-1": {"guild-1"},
// 	}}
// 	client, hub := setupStreamServer(t, resolver)

// 	ctx, cancel := context.WithTimeout(authCtx("user-1"), 5*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamTyping(ctx, &streamv1.StreamTypingRequest{})
// 	require.NoError(t, err)
// 	time.Sleep(200 * time.Millisecond)

// 	// Guild typing
// 	_ = hub.Publish(realtime.GuildChannelTyping("guild-1", "ch-1"), map[string]any{
// 		"channel_id": "ch-1", "guild_id": "guild-1", "user_id": "user-2",
// 	})

// 	evt, err := stream.Recv()
// 	require.NoError(t, err)
// 	assert.Equal(t, "ch-1", evt.ChannelId)
// 	assert.Equal(t, "user-2", evt.UserId)
// }

// // === StreamFriendActivity ===

// func TestStreamFriendActivity(t *testing.T) {
// 	resolver := &mockResolver{}
// 	client, hub := setupStreamServer(t, resolver)

// 	ctx, cancel := context.WithTimeout(authCtx("user-1"), 5*time.Second)
// 	defer cancel()

// 	stream, err := client.StreamFriendActivity(ctx, &streamv1.StreamFriendActivityRequest{})
// 	require.NoError(t, err)
// 	time.Sleep(200 * time.Millisecond)

// 	_ = hub.Publish(realtime.FriendActivity("user-1"), map[string]any{
// 		"event": "presence_update", "user_id": "friend-1", "status": "online",
// 	})

// 	evt, err := stream.Recv()
// 	require.NoError(t, err)
// 	assert.Equal(t, "presence_update", evt.Event)
// 	assert.Equal(t, "friend-1", evt.UserId)
// 	assert.Equal(t, "online", evt.Status)
// }

// // === Unauthenticated ===

// func TestStream_Unauthenticated(t *testing.T) {
// 	resolver := &mockResolver{}
// 	client, _ := setupStreamServer(t, resolver)

// 	ctx := context.Background() // no token

// 	_, err := client.StreamDmChat(ctx, &streamv1.StreamDmChatRequest{})
// 	// Stream opens but first Recv should fail
// 	if err == nil {
// 		t.Log("stream opened, checking recv")
// 	}

// 	_, err = client.StreamTextChannels(ctx, &streamv1.StreamTextChannelsRequest{})
// 	if err != nil {
// 		assert.Equal(t, codes.Unauthenticated, status.Code(err))
// 	}
// }

// // === Test NATS publish/subscribe round-trip with real JSON ===

// func TestNATSRoundTrip(t *testing.T) {
// 	hub, err := realtime.NewLPubSub(config.NATSConfig{URL: "nats://localhost:4222"})
// 	require.NoError(t, err)
// 	defer hub.Close()

// 	sub, err := hub.Subscribe(realtime.GuildChannelMessage("g1", "c1"))
// 	require.NoError(t, err)
// 	defer sub.Unsubscribe()

// 	time.Sleep(100 * time.Millisecond)

// 	msg := map[string]string{"content": "test message", "author_id": "u1"}
// 	err = hub.Publish(realtime.GuildChannelMessage("g1", "c1"), msg)
// 	require.NoError(t, err)

// 	select {
// 	case data := <-sub.Ch:
// 		var received map[string]string
// 		require.NoError(t, json.Unmarshal(data, &received))
// 		assert.Equal(t, "test message", received["content"])
// 		assert.Equal(t, "u1", received["author_id"])
// 	case <-time.After(2 * time.Second):
// 		t.Fatal("timeout waiting for NATS message")
// 	}
// }

// func TestNATSWildcard(t *testing.T) {
// 	hub, err := realtime.NewLPubSub(config.NATSConfig{URL: "nats://localhost:4222"})
// 	require.NoError(t, err)
// 	defer hub.Close()

// 	// Subscribe to ALL messages in guild-1
// 	sub, err := hub.Subscribe(realtime.GuildAllMessages("g1"))
// 	require.NoError(t, err)
// 	defer sub.Unsubscribe()

// 	time.Sleep(100 * time.Millisecond)

// 	// Publish to specific channel
// 	_ = hub.Publish(realtime.GuildChannelMessage("g1", "ch-a"), map[string]string{"content": "msg A"})
// 	_ = hub.Publish(realtime.GuildChannelMessage("g1", "ch-b"), map[string]string{"content": "msg B"})

// 	// Should receive both
// 	var msgs []string
// 	for i := 0; i < 2; i++ {
// 		select {
// 		case data := <-sub.Ch:
// 			var m map[string]string
// 			json.Unmarshal(data, &m)
// 			msgs = append(msgs, m["content"])
// 		case <-time.After(2 * time.Second):
// 			t.Fatal("timeout")
// 		}
// 	}
// 	assert.Contains(t, msgs, "msg A")
// 	assert.Contains(t, msgs, "msg B")
// }
