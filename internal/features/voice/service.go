package voice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/livekit/protocol/auth"
	livekit "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

const tokenTTL = 1 * time.Hour

var (
	ErrInsufficientPermissions = errors.New("insufficient permissions")
	ErrLiveKitUnavailable      = errors.New("livekit not configured")
)

// Service wraps the LiveKit room service client and issues access tokens.
// The SFU (LiveKit) handles all media — this service only deals with auth,
// room lifecycle, and participant bookkeeping.
type Service struct {
	cfg        config.LiveKitConfig
	roomClient *lksdk.RoomServiceClient
	authz      *authz.Client
	nats       *realtime.Hub
}

// NewService constructs a voice service backed by a LiveKit deployment.
func NewService(cfg config.LiveKitConfig, nats *realtime.Hub, authzClient ...*authz.Client) *Service {
	s := &Service{
		cfg:  cfg,
		nats: nats,
	}
	if cfg.HTTPURL != "" && cfg.APIKey != "" && cfg.APISecret != "" {
		s.roomClient = lksdk.NewRoomServiceClient(cfg.HTTPURL, cfg.APIKey, cfg.APISecret)
	}
	if len(authzClient) > 0 {
		s.authz = authzClient[0]
	}
	return s
}

// JoinChannelResult holds the LiveKit connection info returned to clients.
type JoinChannelResult struct {
	URL       string
	Token     string
	Room      string
	ExpiresIn int32
}

// JoinChannel ensures a LiveKit room exists for the given channel and mints
// a short-lived JWT the client can use to connect directly to the SFU.
func (s *Service) JoinChannel(ctx context.Context, userID, guildID, channelID string) (*JoinChannelResult, error) {
	if s.roomClient == nil {
		return nil, ErrLiveKitUnavailable
	}
	if guildID != "" && s.authz != nil && !s.authz.CanViewChannel(ctx, userID, channelID) {
		return nil, ErrInsufficientPermissions
	}

	roomName := channelID

	// Ensure the room exists (idempotent — CreateRoom returns the existing
	// room if it already exists).
	if _, err := s.roomClient.CreateRoom(ctx, &livekit.CreateRoomRequest{
		Name:            roomName,
		EmptyTimeout:    300,
		MaxParticipants: 100,
	}); err != nil {
		return nil, fmt.Errorf("failed to create livekit room: %w", err)
	}

	token, err := s.buildToken(roomName, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to mint token: %w", err)
	}

	_ = s.nats.Publish(realtime.GuildChannelVoice(guildID, channelID), map[string]any{
		"event":      "VOICE_STATE_UPDATE",
		"action":     "join",
		"user_id":    userID,
		"channel_id": channelID,
	})

	return &JoinChannelResult{
		URL:       s.cfg.URL,
		Token:     token,
		Room:      roomName,
		ExpiresIn: int32(tokenTTL.Seconds()),
	}, nil
}

// LeaveChannel kicks the identity out of the LiveKit room. Most clients just
// disconnect locally, but this is exposed so a server-initiated removal works.
func (s *Service) LeaveChannel(ctx context.Context, userID, guildID, channelID string) error {
	if s.roomClient == nil {
		return ErrLiveKitUnavailable
	}
	_, err := s.roomClient.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     channelID,
		Identity: userID,
	})
	// A "participant not found" from LiveKit is fine — user already left.
	if err != nil {
		// swallow and still publish the event so listeners update UI
		_ = err
	}

	_ = s.nats.Publish(realtime.GuildChannelVoice(guildID, channelID), map[string]any{
		"event":      "VOICE_STATE_UPDATE",
		"action":     "leave",
		"user_id":    userID,
		"channel_id": channelID,
	})
	return nil
}

// GetChannelParticipants lists participants in the LiveKit room backing the
// given channel.
func (s *Service) GetChannelParticipants(ctx context.Context, channelID string) ([]*voicev1.VoiceParticipant, error) {
	if s.roomClient == nil {
		return nil, ErrLiveKitUnavailable
	}
	res, err := s.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{
		Room: channelID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list participants: %w", err)
	}

	out := make([]*voicev1.VoiceParticipant, 0, len(res.Participants))
	for _, p := range res.Participants {
		vp := &voicev1.VoiceParticipant{
			UserId:   p.Identity,
			Identity: p.Identity,
			Sid:      p.Sid,
			Name:     p.Name,
		}
		if p.JoinedAt > 0 {
			vp.JoinedAt = timestamppb.New(time.Unix(p.JoinedAt, 0))
		}
		out = append(out, vp)
	}
	return out, nil
}

// buildToken returns a signed JWT granting the user publish+subscribe rights
// in the given room for the configured TTL.
func (s *Service) buildToken(room, identity string) (string, error) {
	at := auth.NewAccessToken(s.cfg.APIKey, s.cfg.APISecret)
	grant := &auth.VideoGrant{
		RoomJoin:     true,
		Room:         room,
		CanPublish:   boolPtr(true),
		CanSubscribe: boolPtr(true),
	}
	at.SetVideoGrant(grant).
		SetIdentity(identity).
		SetValidFor(tokenTTL)
	return at.ToJWT()
}

func boolPtr(b bool) *bool { return &b }
