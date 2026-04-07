package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gatewayv1 "github.com/ananddub/ndiscord_backend/gen/gateway/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/event"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

func init() {
	logger.Init("error")
}

func TestDispatcherMessageCreate(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	// Create a session in guild-1
	session := &Session{
		ID:       "sess-1",
		UserID:   "user-1",
		GuildIDs: []string{"guild-1"},
		Send:     make(chan *gatewayv1.ServerEvent, 10),
	}
	mgr.AddSession(session)

	data, _ := json.Marshal(MessageEventData{
		ID: "msg-1", ChannelID: "ch-1", GuildID: "guild-1", AuthorID: "user-2", Content: "hello",
	})
	env := event.Envelope{Type: event.TopicMessageCreate, GuildID: "guild-1", Data: data}

	err := dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))
	require.NoError(t, err)

	select {
	case evt := <-session.Send:
		assert.Equal(t, gatewayv1.EventType_EVENT_TYPE_MESSAGE_CREATE, evt.Type)
		assert.Equal(t, "msg-1", evt.GetMessageCreate().Id)
		assert.Equal(t, "hello", evt.GetMessageCreate().Content)
	case <-time.After(time.Second):
		t.Fatal("expected event not received")
	}
}

func TestDispatcherMessageUpdate(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	session := &Session{
		ID: "sess-1", UserID: "user-1", GuildIDs: []string{"guild-1"},
		Send: make(chan *gatewayv1.ServerEvent, 10),
	}
	mgr.AddSession(session)

	data, _ := json.Marshal(MessageEventData{
		ID: "msg-1", ChannelID: "ch-1", GuildID: "guild-1", Content: "edited",
	})
	env := event.Envelope{Type: event.TopicMessageUpdate, Data: data}
	err := dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))
	require.NoError(t, err)

	evt := <-session.Send
	assert.Equal(t, gatewayv1.EventType_EVENT_TYPE_MESSAGE_UPDATE, evt.Type)
	assert.Equal(t, "edited", evt.GetMessageUpdate().Content)
}

func TestDispatcherMessageDelete(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	session := &Session{
		ID: "sess-1", UserID: "user-1", GuildIDs: []string{"guild-1"},
		Send: make(chan *gatewayv1.ServerEvent, 10),
	}
	mgr.AddSession(session)

	data, _ := json.Marshal(MessageEventData{ID: "msg-1", ChannelID: "ch-1", GuildID: "guild-1"})
	env := event.Envelope{Type: event.TopicMessageDelete, Data: data}
	err := dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))
	require.NoError(t, err)

	evt := <-session.Send
	assert.Equal(t, gatewayv1.EventType_EVENT_TYPE_MESSAGE_DELETE, evt.Type)
	assert.Equal(t, "msg-1", evt.GetMessageDelete().Id)
}

func TestDispatcherTypingStart(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	session := &Session{
		ID: "sess-1", UserID: "user-1", GuildIDs: []string{"guild-1"},
		Send: make(chan *gatewayv1.ServerEvent, 10),
	}
	mgr.AddSession(session)

	data, _ := json.Marshal(TypingEventData{ChannelID: "ch-1", GuildID: "guild-1", UserID: "user-2"})
	env := event.Envelope{Type: event.TopicTyping, Data: data}
	err := dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))
	require.NoError(t, err)

	evt := <-session.Send
	assert.Equal(t, gatewayv1.EventType_EVENT_TYPE_TYPING_START, evt.Type)
	assert.Equal(t, "user-2", evt.GetTypingStart().UserId)
}

func TestDispatcherGuildMemberAdd(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	session := &Session{
		ID: "sess-1", UserID: "user-1", GuildIDs: []string{"guild-1"},
		Send: make(chan *gatewayv1.ServerEvent, 10),
	}
	mgr.AddSession(session)

	data, _ := json.Marshal(GuildEventData{Action: "member_add", GuildID: "guild-1", UserID: "user-2"})
	env := event.Envelope{Type: event.TopicGuildEvents, Data: data}
	err := dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))
	require.NoError(t, err)

	evt := <-session.Send
	assert.Equal(t, gatewayv1.EventType_EVENT_TYPE_GUILD_MEMBER_ADD, evt.Type)
	assert.Equal(t, "user-2", evt.GetGuildMemberAdd().UserId)
}

func TestDispatcherChannelCreate(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	session := &Session{
		ID: "sess-1", UserID: "user-1", GuildIDs: []string{"guild-1"},
		Send: make(chan *gatewayv1.ServerEvent, 10),
	}
	mgr.AddSession(session)

	data, _ := json.Marshal(ChannelEventData{Action: "create", ChannelID: "ch-1", GuildID: "guild-1", Name: "general", Type: 1})
	env := event.Envelope{Type: event.TopicChannelEvents, Data: data}
	err := dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))
	require.NoError(t, err)

	evt := <-session.Send
	assert.Equal(t, gatewayv1.EventType_EVENT_TYPE_CHANNEL_CREATE, evt.Type)
	assert.Equal(t, "general", evt.GetChannelCreate().Name)
}

func TestDispatcherVoiceStateUpdate(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	session := &Session{
		ID: "sess-1", UserID: "user-1", GuildIDs: []string{"guild-1"},
		Send: make(chan *gatewayv1.ServerEvent, 10),
	}
	mgr.AddSession(session)

	data, _ := json.Marshal(VoiceStateEventData{GuildID: "guild-1", ChannelID: "ch-1", UserID: "user-2", SelfMute: true})
	env := event.Envelope{Type: event.TopicVoiceState, Data: data}
	err := dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))
	require.NoError(t, err)

	evt := <-session.Send
	assert.Equal(t, gatewayv1.EventType_EVENT_TYPE_VOICE_STATE_UPDATE, evt.Type)
	assert.True(t, evt.GetVoiceStateUpdate().SelfMute)
}

func TestDispatcherOnlyBroadcastsToCorrectGuild(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	s1 := &Session{ID: "s1", UserID: "u1", GuildIDs: []string{"guild-1"}, Send: make(chan *gatewayv1.ServerEvent, 10)}
	s2 := &Session{ID: "s2", UserID: "u2", GuildIDs: []string{"guild-2"}, Send: make(chan *gatewayv1.ServerEvent, 10)}
	mgr.AddSession(s1)
	mgr.AddSession(s2)

	data, _ := json.Marshal(MessageEventData{ID: "msg-1", ChannelID: "ch-1", GuildID: "guild-1", Content: "hi"})
	env := event.Envelope{Type: event.TopicMessageCreate, Data: data}
	_ = dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))

	// s1 should get it (guild-1)
	select {
	case evt := <-s1.Send:
		assert.Equal(t, "msg-1", evt.GetMessageCreate().Id)
	case <-time.After(time.Second):
		t.Fatal("s1 should receive event")
	}

	// s2 should NOT get it (guild-2)
	select {
	case <-s2.Send:
		t.Fatal("s2 should not receive event for guild-1")
	case <-time.After(100 * time.Millisecond):
		// correct, no event
	}
}

func TestDispatcherUnknownEventType(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	env := event.Envelope{Type: "unknown.type", Data: json.RawMessage(`{}`)}
	err := dispatcher.handleEvent(context.TODO(), nil, mustMarshal(env))
	assert.NoError(t, err)
}

func TestDispatcherMalformedJSON(t *testing.T) {
	mgr := NewSessionManager()
	dispatcher := NewDispatcher(mgr, nil, nil, nil)

	err := dispatcher.handleEvent(context.TODO(), nil, []byte("not json"))
	assert.NoError(t, err) // should not return error, just log
}

func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
