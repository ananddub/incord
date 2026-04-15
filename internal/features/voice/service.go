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
	ErrNotDMMember             = errors.New("not a member of this DM channel")
	ErrDMResolverMissing       = errors.New("dm resolver not configured")
)

// DMMembershipResolver lets the voice service check DM channel membership
// and fan out call-signalling events to every member without a hard
// dependency on the channel package.
type DMMembershipResolver interface {
	// IsMember reports whether userID is a member of the given DM channel.
	IsDMMember(ctx context.Context, channelID, userID string) (bool, error)
	// Members returns every user_id in the given DM channel.
	DMMembers(ctx context.Context, channelID string) ([]string, error)
}

// Service wraps the LiveKit room service client and issues access tokens.
// The SFU (LiveKit) handles all media — this service only deals with auth,
// room lifecycle, and participant bookkeeping.
type Service struct {
	cfg        config.LiveKitConfig
	roomClient *lksdk.RoomServiceClient
	authz      *authz.Client
	nats       *realtime.Hub
	dm         DMMembershipResolver
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

// SetDMResolver wires the DM membership resolver used by DM call RPCs.
func (s *Service) SetDMResolver(r DMMembershipResolver) { s.dm = r }

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

// ── DM calls ───────────────────────────────────────────────────────────────

// StartDMCall is called by the caller to initiate a voice call in a DM
// channel. The caller gets a LiveKit token immediately and every other
// member receives a "call_incoming" event on their DmCall subject so their
// client can ring.
func (s *Service) StartDMCall(ctx context.Context, callerID, channelID string, video bool) (*JoinChannelResult, error) {
	if s.roomClient == nil {
		return nil, ErrLiveKitUnavailable
	}
	if s.dm == nil {
		return nil, ErrDMResolverMissing
	}

	ok, err := s.dm.IsDMMember(ctx, channelID, callerID)
	if err != nil {
		return nil, fmt.Errorf("membership check failed: %w", err)
	}
	if !ok {
		return nil, ErrNotDMMember
	}

	roomName := channelID
	if _, err := s.roomClient.CreateRoom(ctx, &livekit.CreateRoomRequest{
		Name:            roomName,
		EmptyTimeout:    60,
		MaxParticipants: 10,
	}); err != nil {
		return nil, fmt.Errorf("failed to create livekit room: %w", err)
	}

	token, err := s.buildToken(roomName, callerID)
	if err != nil {
		return nil, fmt.Errorf("failed to mint token: %w", err)
	}

	// Fan out "call_incoming" to every member — including the caller
	// themselves so their other devices can mirror the active-call state
	// (e.g. show "call in progress on another device").
	members, _ := s.dm.DMMembers(ctx, channelID)
	payload := map[string]any{
		"type":       "call_incoming",
		"channel_id": channelID,
		"caller_id":  callerID,
		"video":      video,
	}
	for _, mid := range members {
		_ = s.nats.Publish(realtime.DmCall(mid), payload)
	}

	return &JoinChannelResult{
		URL:       s.cfg.URL,
		Token:     token,
		Room:      roomName,
		ExpiresIn: int32(tokenTTL.Seconds()),
	}, nil
}

// JoinDMCall is called by a ringing recipient to accept. Mints a token and
// publishes "call_accepted" to every member so clients can transition the
// ringing UI to in-call.
func (s *Service) JoinDMCall(ctx context.Context, userID, channelID string, video bool) (*JoinChannelResult, error) {
	if s.roomClient == nil {
		return nil, ErrLiveKitUnavailable
	}
	if s.dm == nil {
		return nil, ErrDMResolverMissing
	}

	ok, err := s.dm.IsDMMember(ctx, channelID, userID)
	if err != nil {
		return nil, fmt.Errorf("membership check failed: %w", err)
	}
	if !ok {
		return nil, ErrNotDMMember
	}

	roomName := channelID
	// CreateRoom is idempotent — caller may have already created it but the
	// room could also have been reaped if empty, so re-create defensively.
	if _, err := s.roomClient.CreateRoom(ctx, &livekit.CreateRoomRequest{
		Name:            roomName,
		EmptyTimeout:    60,
		MaxParticipants: 10,
	}); err != nil {
		return nil, fmt.Errorf("failed to create livekit room: %w", err)
	}

	token, err := s.buildToken(roomName, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to mint token: %w", err)
	}

	members, _ := s.dm.DMMembers(ctx, channelID)
	payload := map[string]any{
		"type":           "call_accepted",
		"channel_id":     channelID,
		"participant_id": userID,
		"video":          video,
	}
	for _, mid := range members {
		_ = s.nats.Publish(realtime.DmCall(mid), payload)
	}

	return &JoinChannelResult{
		URL:       s.cfg.URL,
		Token:     token,
		Room:      roomName,
		ExpiresIn: int32(tokenTTL.Seconds()),
	}, nil
}

// RejectDMCall notifies every member (notably the caller) that this user
// declined. No LiveKit room state is touched.
func (s *Service) RejectDMCall(ctx context.Context, userID, channelID string) error {
	if s.dm == nil {
		return ErrDMResolverMissing
	}
	ok, err := s.dm.IsDMMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("membership check failed: %w", err)
	}
	if !ok {
		return ErrNotDMMember
	}

	members, _ := s.dm.DMMembers(ctx, channelID)
	payload := map[string]any{
		"type":           "call_rejected",
		"channel_id":     channelID,
		"participant_id": userID,
	}
	for _, mid := range members {
		_ = s.nats.Publish(realtime.DmCall(mid), payload)
	}
	return nil
}

// LeaveDMCall removes the user from the LiveKit room and notifies every
// member. If the room empties out, a "call_ended" event is fanned out too.
func (s *Service) LeaveDMCall(ctx context.Context, userID, channelID string) error {
	if s.roomClient == nil {
		return ErrLiveKitUnavailable
	}
	if s.dm == nil {
		return ErrDMResolverMissing
	}
	ok, err := s.dm.IsDMMember(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("membership check failed: %w", err)
	}
	if !ok {
		return ErrNotDMMember
	}

	_, _ = s.roomClient.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     channelID,
		Identity: userID,
	})

	members, _ := s.dm.DMMembers(ctx, channelID)
	leftPayload := map[string]any{
		"type":           "participant_left",
		"channel_id":     channelID,
		"participant_id": userID,
	}
	for _, mid := range members {
		_ = s.nats.Publish(realtime.DmCall(mid), leftPayload)
	}

	// If the room is now empty, emit a terminal "call_ended" event so
	// recipients that were still ringing can dismiss their UI.
	remaining, err := s.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{
		Room: channelID,
	})
	if err == nil && len(remaining.Participants) == 0 {
		endPayload := map[string]any{
			"type":       "call_ended",
			"channel_id": channelID,
		}
		for _, mid := range members {
			_ = s.nats.Publish(realtime.DmCall(mid), endPayload)
		}
	}

	return nil
}
