package gateway

import (
	"context"
	"errors"
	"io"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gatewayv1 "github.com/ananddub/ndiscord_backend/gen/gateway/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// PresenceUpdater sets a user's presence to offline.
type PresenceUpdater interface {
	SetOffline(ctx context.Context, userID string) error
}

var errSessionNotFound = errors.New("session not found")

// Handler implements the GatewayServiceServer gRPC interface.
type Handler struct {
	gatewayv1.UnimplementedGatewayServiceServer
	service         *Service
	presenceUpdater PresenceUpdater
}

// NewHandler creates a new gateway Handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// SetPresenceUpdater sets the presence updater (avoids circular deps in constructor).
func (h *Handler) SetPresenceUpdater(p PresenceUpdater) { h.presenceUpdater = p }

// Connect implements the bidirectional streaming RPC.
func (h *Handler) Connect(stream grpc.BidiStreamingServer[gatewayv1.ClientMessage, gatewayv1.ServerEvent]) error {
	var session *Session

	// errCh carries the error from the reader goroutine.
	errCh := make(chan error, 1)

	// Reader goroutine: reads client messages and dispatches them.
	go func() {
		for {
			msg, err := stream.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					errCh <- nil
				} else {
					errCh <- err
				}
				return
			}

			switch payload := msg.GetPayload().(type) {
			case *gatewayv1.ClientMessage_Identify:
				if session != nil {
					errCh <- status.Error(codes.AlreadyExists, "already identified")
					return
				}
				s, readyEvt, err := h.service.HandleIdentify(payload.Identify)
				if err != nil {
					errCh <- status.Error(codes.Unauthenticated, "invalid token")
					return
				}
				session = s
				select {
				case session.Send <- readyEvt:
				default:
				}

			case *gatewayv1.ClientMessage_Heartbeat:
				if session == nil {
					errCh <- status.Error(codes.FailedPrecondition, "must identify first")
					return
				}
				ack := h.service.HandleHeartbeat(session, payload.Heartbeat)
				select {
				case session.Send <- ack:
				default:
				}

			case *gatewayv1.ClientMessage_Resume:
				if session != nil {
					errCh <- status.Error(codes.AlreadyExists, "already identified")
					return
				}
				s, resumedEvt, err := h.service.HandleResume(payload.Resume)
				if err != nil {
					if errors.Is(err, errSessionNotFound) {
						errCh <- status.Error(codes.NotFound, "session not found, please re-identify")
					} else {
						errCh <- status.Error(codes.Unauthenticated, "invalid token")
					}
					return
				}
				session = s
				select {
				case session.Send <- resumedEvt:
				default:
				}

			case *gatewayv1.ClientMessage_UpdatePresence:
				if session == nil {
					continue
				}
				h.service.HandleUpdatePresence(session, payload.UpdatePresence)

			case *gatewayv1.ClientMessage_UpdateVoiceState:
				if session == nil {
					continue
				}
				h.service.HandleUpdateVoiceState(session, payload.UpdateVoiceState)

			default:
				logger.Log.Warn().Msg("unknown client message payload type")
			}
		}
	}()

	// Wait until the session is established (Identify or Resume).
	// Until then, only the reader goroutine or context cancellation can end us.
	for session == nil {
		select {
		case err := <-errCh:
			return err
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}

	// Writer loop: sends events from session.Send to the stream.
	for {
		select {
		case evt, ok := <-session.Send:
			if !ok {
				h.disconnect(stream.Context(), session)
				return nil
			}
			if err := stream.Send(evt); err != nil {
				h.disconnect(stream.Context(), session)
				return err
			}

		case err := <-errCh:
			h.disconnect(stream.Context(), session)
			if err != nil {
				return err
			}
			return nil

		case <-stream.Context().Done():
			h.disconnect(stream.Context(), session)
			return stream.Context().Err()
		}
	}
}

// disconnect removes the session and sets the user's presence to offline.
func (h *Handler) disconnect(ctx context.Context, session *Session) {
	h.service.Disconnect(session.ID)
	if h.presenceUpdater != nil {
		// Use a background context in case the stream context is already cancelled.
		bgCtx := context.Background()
		if err := h.presenceUpdater.SetOffline(bgCtx, session.UserID); err != nil {
			logger.Log.Warn().Err(err).Str("user_id", session.UserID).Msg("failed to set offline on disconnect")
		}
	}
}
