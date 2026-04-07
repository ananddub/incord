package gateway

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gatewayv1 "github.com/ananddub/ndiscord_backend/gen/gateway/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

func init() {
	// The session manager logs via logger.Log; initialise it so tests don't panic.
	logger.Init("debug")
}

func makeSession(id, userID string, guildIDs []string) *Session {
	return &Session{
		ID:       id,
		UserID:   userID,
		GuildIDs: guildIDs,
		Send:     make(chan *gatewayv1.ServerEvent, 16),
	}
}

func TestAddAndGetSession(t *testing.T) {
	sm := NewSessionManager()
	s := makeSession("sess-1", "user-1", []string{"guild-a"})

	sm.AddSession(s)

	got, ok := sm.GetSession("sess-1")
	require.True(t, ok)
	assert.Equal(t, "sess-1", got.ID)
	assert.Equal(t, "user-1", got.UserID)
}

func TestGetSession_NotFound(t *testing.T) {
	sm := NewSessionManager()

	_, ok := sm.GetSession("nonexistent")
	assert.False(t, ok)
}

func TestRemoveSession(t *testing.T) {
	sm := NewSessionManager()
	s := makeSession("sess-1", "user-1", []string{"guild-a"})
	sm.AddSession(s)

	sm.RemoveSession("sess-1")

	_, ok := sm.GetSession("sess-1")
	assert.False(t, ok)

	// User sessions should also be cleaned up.
	sessions := sm.GetUserSessions("user-1")
	assert.Empty(t, sessions)
}

func TestRemoveSession_Idempotent(t *testing.T) {
	sm := NewSessionManager()
	// Removing a nonexistent session should not panic.
	sm.RemoveSession("does-not-exist")
}

func TestGetUserSessions(t *testing.T) {
	sm := NewSessionManager()
	s1 := makeSession("sess-1", "user-1", []string{"guild-a"})
	s2 := makeSession("sess-2", "user-1", []string{"guild-a"})
	s3 := makeSession("sess-3", "user-2", []string{"guild-a"})

	sm.AddSession(s1)
	sm.AddSession(s2)
	sm.AddSession(s3)

	sessions := sm.GetUserSessions("user-1")
	assert.Len(t, sessions, 2)

	sessions = sm.GetUserSessions("user-2")
	assert.Len(t, sessions, 1)

	sessions = sm.GetUserSessions("user-999")
	assert.Nil(t, sessions)
}

func TestRemoveSession_PartialUserCleanup(t *testing.T) {
	sm := NewSessionManager()
	s1 := makeSession("sess-1", "user-1", []string{"guild-a"})
	s2 := makeSession("sess-2", "user-1", []string{"guild-a"})

	sm.AddSession(s1)
	sm.AddSession(s2)

	// Remove one session; the user should still have one left.
	sm.RemoveSession("sess-1")

	sessions := sm.GetUserSessions("user-1")
	require.Len(t, sessions, 1)
	assert.Equal(t, "sess-2", sessions[0].ID)
}

func TestBroadcastToGuild(t *testing.T) {
	sm := NewSessionManager()

	s1 := makeSession("sess-1", "user-1", []string{"guild-a"})
	s2 := makeSession("sess-2", "user-2", []string{"guild-a"})
	s3 := makeSession("sess-3", "user-3", []string{"guild-b"}) // different guild

	sm.AddSession(s1)
	sm.AddSession(s2)
	sm.AddSession(s3)

	event := &gatewayv1.ServerEvent{
		Data: &gatewayv1.ServerEvent_HeartbeatAck{
			HeartbeatAck: &gatewayv1.HeartbeatAck{Sequence: 42},
		},
	}

	sm.BroadcastToGuild("guild-a", event)

	// s1 and s2 should receive the event.
	got1 := <-s1.Send
	assert.Equal(t, int64(42), got1.GetHeartbeatAck().GetSequence())

	got2 := <-s2.Send
	assert.Equal(t, int64(42), got2.GetHeartbeatAck().GetSequence())

	// s3 should NOT receive anything.
	select {
	case <-s3.Send:
		t.Fatal("session in guild-b should not receive guild-a broadcast")
	default:
		// expected
	}
}

func TestBroadcastToGuild_NoMembers(t *testing.T) {
	sm := NewSessionManager()
	event := &gatewayv1.ServerEvent{}

	// Should not panic on an empty guild.
	sm.BroadcastToGuild("empty-guild", event)
}

func TestBroadcastToGuild_BufferFull(t *testing.T) {
	sm := NewSessionManager()

	// Create a session with a tiny buffer.
	s := &Session{
		ID:       "sess-1",
		UserID:   "user-1",
		GuildIDs: []string{"guild-a"},
		Send:     make(chan *gatewayv1.ServerEvent, 1),
	}
	sm.AddSession(s)

	event := &gatewayv1.ServerEvent{}
	// Fill the buffer.
	sm.BroadcastToGuild("guild-a", event)
	// Second broadcast should drop (not block).
	sm.BroadcastToGuild("guild-a", event)

	// Drain the one that made it.
	<-s.Send
}
