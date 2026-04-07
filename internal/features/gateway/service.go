package gateway

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	gatewayv1 "github.com/ananddub/ndiscord_backend/gen/gateway/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

const (
	heartbeatInterval = 45 * time.Second
	sendBufferSize    = 256
)

// Service contains the business logic for the gateway feature.
type Service struct {
	sessions  *SessionManager
	jwtSecret string
}

// NewService creates a new gateway Service.
func NewService(sessions *SessionManager, jwtSecret string) *Service {
	return &Service{
		sessions:  sessions,
		jwtSecret: jwtSecret,
	}
}

// HandleIdentify processes the Identify message from a client.
// It validates the token, creates a session, and returns a Ready event.
func (s *Service) HandleIdentify(identify *gatewayv1.Identify) (*Session, *gatewayv1.ServerEvent, error) {
	userID, err := validateToken(identify.GetToken(), s.jwtSecret)
	if err != nil {
		return nil, nil, err
	}

	sessionID := uuid.New().String()
	session := &Session{
		ID:       sessionID,
		UserID:   userID,
		GuildIDs: nil, // Will be populated from DB in a production system
		Send:     make(chan *gatewayv1.ServerEvent, sendBufferSize),
	}
	session.LastHeartbeat.Store(time.Now().Unix())

	s.sessions.AddSession(session)

	readyEvent := &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_READY,
		Sequence:  session.Sequence.Add(1),
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_Ready{
			Ready: &gatewayv1.Ready{
				SessionId:           sessionID,
				UserId:              userID,
				GuildIds:            session.GuildIDs,
				HeartbeatIntervalMs: heartbeatInterval.Milliseconds(),
			},
		},
	}

	return session, readyEvent, nil
}

// HandleHeartbeat processes a Heartbeat message and returns a HeartbeatAck event.
func (s *Service) HandleHeartbeat(session *Session, hb *gatewayv1.Heartbeat) *gatewayv1.ServerEvent {
	session.LastHeartbeat.Store(time.Now().Unix())

	return &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_HEARTBEAT_ACK,
		Sequence:  session.Sequence.Add(1),
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_HeartbeatAck{
			HeartbeatAck: &gatewayv1.HeartbeatAck{
				Sequence: hb.GetSequence(),
			},
		},
	}
}

// HandleResume attempts to resume a previously disconnected session.
func (s *Service) HandleResume(resume *gatewayv1.Resume) (*Session, *gatewayv1.ServerEvent, error) {
	_, err := validateToken(resume.GetToken(), s.jwtSecret)
	if err != nil {
		return nil, nil, err
	}

	existing, ok := s.sessions.GetSession(resume.GetSessionId())
	if !ok {
		// Session not found; client must re-identify
		return nil, nil, errSessionNotFound
	}

	existing.LastHeartbeat.Store(time.Now().Unix())

	resumedEvent := &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_RESUMED,
		Sequence:  existing.Sequence.Add(1),
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_Resumed{
			Resumed: &gatewayv1.Resumed{},
		},
	}

	return existing, resumedEvent, nil
}

// HandleUpdatePresence logs a presence update from the gateway stream.
func (s *Service) HandleUpdatePresence(session *Session, _ *gatewayv1.UpdatePresence) {
	logger.Log.Debug().
		Str("session_id", session.ID).
		Str("user_id", session.UserID).
		Msg("presence update received via gateway")
}

// HandleUpdateVoiceState logs a voice state update from the gateway stream.
func (s *Service) HandleUpdateVoiceState(session *Session, _ *gatewayv1.UpdateVoiceState) {
	logger.Log.Debug().
		Str("session_id", session.ID).
		Str("user_id", session.UserID).
		Msg("voice state update received via gateway")
}

// Disconnect removes the session and performs cleanup.
func (s *Service) Disconnect(sessionID string) {
	s.sessions.RemoveSession(sessionID)
}

// validateToken parses a JWT token string and returns the user ID from the "sub" claim.
func validateToken(tokenStr, secret string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token claims")
	}

	userID, ok := claims["sub"].(string)
	if !ok {
		return "", fmt.Errorf("missing user id in token")
	}

	return userID, nil
}
