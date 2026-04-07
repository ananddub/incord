package gateway

import (
	"sync"
	"sync/atomic"

	gatewayv1 "github.com/ananddub/ndiscord_backend/gen/gateway/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// Session represents a single connected client.
type Session struct {
	ID            string
	UserID        string
	GuildIDs      []string
	Sequence      atomic.Int64
	LastHeartbeat atomic.Int64 // unix timestamp of last heartbeat
	Send          chan *gatewayv1.ServerEvent
}

// SessionManager manages all active gateway sessions.
type SessionManager struct {
	mu           sync.RWMutex
	sessions     map[string]*Session            // session_id -> Session
	userSessions map[string]map[string]*Session  // user_id -> session_id -> Session
	guildMembers map[string]map[string]struct{}  // guild_id -> set of user_ids
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:     make(map[string]*Session),
		userSessions: make(map[string]map[string]*Session),
		guildMembers: make(map[string]map[string]struct{}),
	}
}

// AddSession registers a session. It also indexes the session by user and guilds.
func (m *SessionManager) AddSession(s *Session) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sessions[s.ID] = s

	if m.userSessions[s.UserID] == nil {
		m.userSessions[s.UserID] = make(map[string]*Session)
	}
	m.userSessions[s.UserID][s.ID] = s

	for _, guildID := range s.GuildIDs {
		if m.guildMembers[guildID] == nil {
			m.guildMembers[guildID] = make(map[string]struct{})
		}
		m.guildMembers[guildID][s.UserID] = struct{}{}
	}

	logger.Log.Info().Str("session_id", s.ID).Str("user_id", s.UserID).Msg("session added")
}

// RemoveSession unregisters a session and cleans up all indexes.
func (m *SessionManager) RemoveSession(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[sessionID]
	if !ok {
		return
	}

	delete(m.sessions, sessionID)

	if userMap, exists := m.userSessions[s.UserID]; exists {
		delete(userMap, sessionID)
		if len(userMap) == 0 {
			delete(m.userSessions, s.UserID)
			// Remove user from guilds only when they have no remaining sessions
			for _, guildID := range s.GuildIDs {
				if members, ok := m.guildMembers[guildID]; ok {
					delete(members, s.UserID)
					if len(members) == 0 {
						delete(m.guildMembers, guildID)
					}
				}
			}
		}
	}

	logger.Log.Info().Str("session_id", s.ID).Str("user_id", s.UserID).Msg("session removed")
}

// GetSession returns a session by its ID.
func (m *SessionManager) GetSession(sessionID string) (*Session, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[sessionID]
	return s, ok
}

// GetUserSessions returns all sessions for a given user.
func (m *SessionManager) GetUserSessions(userID string) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()

	userMap, ok := m.userSessions[userID]
	if !ok {
		return nil
	}

	sessions := make([]*Session, 0, len(userMap))
	for _, s := range userMap {
		sessions = append(sessions, s)
	}
	return sessions
}

// BroadcastToGuild sends an event to every session of every user in the guild.
func (m *SessionManager) BroadcastToGuild(guildID string, event *gatewayv1.ServerEvent) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	members, ok := m.guildMembers[guildID]
	if !ok {
		return
	}

	for userID := range members {
		userMap, exists := m.userSessions[userID]
		if !exists {
			continue
		}
		for _, s := range userMap {
			select {
			case s.Send <- event:
			default:
				logger.Log.Warn().Str("session_id", s.ID).Msg("session send buffer full, dropping event")
			}
		}
	}
}

// BroadcastToChannel sends an event to all sessions that belong to guilds.
// Since channel membership is guild-scoped, this broadcasts to the guild that
// owns the channel. The caller is expected to pass the guild ID.
func (m *SessionManager) BroadcastToChannel(guildID string, event *gatewayv1.ServerEvent) {
	m.BroadcastToGuild(guildID, event)
}
