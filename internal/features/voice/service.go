package voice

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/livekit/protocol/auth"
	livekit "github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"

	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
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

type DMMembershipResolver interface {
	IsDMMember(ctx context.Context, channelID, userID string) (bool, error)
	DMMembers(ctx context.Context, channelID string) ([]string, error)
}

type Service struct {
	cfg        config.LiveKitConfig
	roomClient *lksdk.RoomServiceClient
	authz      *authz.Client
	lpb        *realtime.LPubSub
	redis      *redis.Client
	dm         DMMembershipResolver
	profile    UserProfileResolver
}

func NewService(cfg config.LiveKitConfig, lpb *realtime.LPubSub, rdb *redis.Client, authzClient ...*authz.Client) *Service {
	s := &Service{
		cfg:   cfg,
		lpb:   lpb,
		redis: rdb,
	}
	if cfg.HTTPURL != "" && cfg.APIKey != "" && cfg.APISecret != "" {
		s.roomClient = lksdk.NewRoomServiceClient(cfg.HTTPURL, cfg.APIKey, cfg.APISecret)
	}
	if len(authzClient) > 0 {
		s.authz = authzClient[0]
	}
	return s
}

func (s *Service) SetDMResolver(r DMMembershipResolver) { s.dm = r }

type JoinChannelResult struct {
	URL       string
	Token     string
	Room      string
	ExpiresIn int32
}

func (s *Service) JoinChannel(ctx context.Context, userID, guildID, channelID string) (*JoinChannelResult, error) {
	if s.roomClient == nil {
		return nil, ErrLiveKitUnavailable
	}
	// Voice joins gate on CONNECT (Discord's dedicated voice-join perm)
	// rather than the channel-level "viewer" relation, so a role can
	// see a voice channel without being allowed to join it.
	if guildID != "" && s.authz != nil {
		if !s.authz.CanViewChannel(ctx, userID, channelID) || !s.authz.CanConnect(ctx, userID, guildID) {
			return nil, ErrInsufficientPermissions
		}
	}

	roomName := channelID

	roomMeta := fmt.Sprintf(`{"guildId":"%s","channelId":"%s"}`, guildID, channelID)
	if _, err := s.roomClient.CreateRoom(ctx, &livekit.CreateRoomRequest{
		Name:            roomName,
		EmptyTimeout:    300,
		MaxParticipants: 100,
		Metadata:        roomMeta,
	}); err != nil {
		return nil, fmt.Errorf("failed to create livekit room: %w", err)
	}

	token, err := s.buildToken(roomName, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to mint token: %w", err)
	}

	username, avatarURL := "", ""
	if s.profile != nil {
		username, avatarURL = s.profile.LookupBasicProfile(ctx, userID)
	}
	_ = s.SetParticipant(ctx, ParticipantState{
		UserID:      userID,
		Username:    username,
		DisplayName: username,
		AvatarURL:   avatarURL,
		GuildID:     guildID,
		ChannelID:   channelID,
	})
	_, _ = s.EnsureActiveSince(ctx, channelID)

	s.broadcastChannelState(ctx, time.Now(), channelID, guildID)

	return &JoinChannelResult{
		URL:       s.cfg.URL,
		Token:     token,
		Room:      roomName,
		ExpiresIn: int32(tokenTTL.Seconds()),
	}, nil
}

func (s *Service) LeaveChannel(ctx context.Context, userID, guildID, channelID string) error {
	if s.roomClient == nil {
		return ErrLiveKitUnavailable
	}
	_, _ = s.roomClient.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room:     channelID,
		Identity: userID,
	})

	// Remove from Redis and broadcast updated participant list.
	_ = s.RemoveParticipant(ctx, channelID, userID)
	s.broadcastChannelState(ctx, time.Now(), channelID, guildID)
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

// UserProfileResolver looks up the basic profile to bake into LiveKit
// participant metadata so other participants can render names/avatars
// without a separate user-service call.
type UserProfileResolver interface {
	LookupBasicProfile(ctx context.Context, userID string) (username, avatarURL string)
}

// SetProfileResolver wires the user profile resolver.
func (s *Service) SetProfileResolver(r UserProfileResolver) { s.profile = r }

// buildToken returns a signed JWT granting the user publish+subscribe rights
// in the given room for the configured TTL. User metadata (profile JSON) is
// embedded so other participants see it immediately on join.
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

	// Embed user profile as participant metadata — LiveKit stores it and
	// the webhook relays it, so every client sees username + avatar
	// without extra lookups.
	if s.profile != nil {
		username, avatarURL := s.profile.LookupBasicProfile(context.Background(), identity)
		meta := fmt.Sprintf(`{"userId":"%s","username":"%s","avatarUrl":"%s"}`, identity, username, avatarURL)
		at.SetMetadata(meta)
	}

	return at.ToJWT()
}

func boolPtr(b bool) *bool { return &b }

type VoiceSnapshot struct {
	svc *Service
}

func NewVoiceSnapshot(svc *Service) *VoiceSnapshot {
	return &VoiceSnapshot{svc: svc}
}

func (v *VoiceSnapshot) GetChannelParticipants(ctx context.Context, channelID string) ([]*streamv1.VoiceStateEvent, error) {
	var liveParticipants map[string]*livekit.ParticipantInfo
	if v.svc.roomClient != nil {
		res, err := v.svc.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{
			Room: channelID,
		})
		if err == nil && res != nil {
			liveParticipants = make(map[string]*livekit.ParticipantInfo, len(res.Participants))
			for _, p := range res.Participants {
				liveParticipants[p.Identity] = p
			}
		}
	}
	// Room active-since = first joiner's timestamp stored in Redis.
	roomActiveSince := v.svc.GetActiveSince(ctx, channelID)

	// If LiveKit says nobody's connected yet (user may have just called
	// JoinChannel and is still negotiating WebRTC), fall back to Redis
	// state. Don't clear Redis from a read path — that's handled by
	// explicit LeaveChannel + participant_left / room_finished webhooks.
	if len(liveParticipants) == 0 {
		redisStates, _ := v.svc.GetChannelState(ctx, channelID)
		roomActiveSince := v.svc.GetActiveSince(ctx, channelID)
		events := make([]*streamv1.VoiceStateEvent, len(redisStates))
		for i, rs := range redisStates {
			events[i] = &streamv1.VoiceStateEvent{
				Event:           streamv1.VoiceEvent_VOICE_EVENT_STATE_UPDATE,
				Action:          streamv1.VoiceAction_VOICE_ACTION_STATE_SYNC,
				ChannelId:       channelID,
				GuildId:         rs.GuildID,
				UserId:          rs.UserID,
				Name:            rs.DisplayName,
				SelfMute:        rs.SelfMute,
				SelfDeaf:        rs.SelfDeaf,
				Video:           rs.Video,
				Streaming:       rs.ScreenShare,
				Metadata:        fmt.Sprintf(`{"userId":"%s","username":"%s","displayName":"%s","avatarUrl":"%s"}`, rs.UserID, rs.Username, rs.DisplayName, rs.AvatarURL),
				RoomActiveSince: roomActiveSince,
			}
		}
		return events, nil
	}

	// Step 2: Load Redis state for voice toggles (mute/deaf/video/screen)
	redisStates, _ := v.svc.GetChannelState(ctx, channelID)
	redisMap := make(map[string]ParticipantState, len(redisStates))
	for _, s := range redisStates {
		redisMap[s.UserID] = s
	}

	// Step 3: Clean stale Redis entries (in Redis but not in LiveKit)
	for uid := range redisMap {
		if _, ok := liveParticipants[uid]; !ok {
			_ = v.svc.RemoveParticipant(ctx, channelID, uid)
		}
	}

	// Step 4: Build events — LiveKit = source of truth for who's in,
	// Redis = source of truth for mute/deaf/video/screen state.
	var events []*streamv1.VoiceStateEvent
	for uid, lp := range liveParticipants {
		rs, hasRedis := redisMap[uid]
		evt := &streamv1.VoiceStateEvent{
			Event:           streamv1.VoiceEvent_VOICE_EVENT_STATE_UPDATE,
			Action:          streamv1.VoiceAction_VOICE_ACTION_STATE_SYNC,
			ChannelId:       channelID,
			UserId:          uid,
			Name:            lp.Name,
			Sid:             lp.Sid,
			Metadata:        lp.Metadata,
			RoomActiveSince: roomActiveSince,
		}
		if hasRedis {
			evt.GuildId = rs.GuildID
			evt.Name = rs.DisplayName
			evt.SelfMute = rs.SelfMute
			evt.SelfDeaf = rs.SelfDeaf
			evt.Video = rs.Video
			evt.Streaming = rs.ScreenShare
			evt.Metadata = fmt.Sprintf(`{"userId":"%s","username":"%s","displayName":"%s","avatarUrl":"%s"}`, rs.UserID, rs.Username, rs.DisplayName, rs.AvatarURL)
		}
		if lp.JoinedAt > 0 {
			evt.Timestamp = timestamppb.New(time.Unix(lp.JoinedAt, 0))
		}
		events = append(events, evt)
	}
	return events, nil
}

// GetGuildVoiceChannelIDs scans Redis for active voice channels in a guild.
func (v *VoiceSnapshot) GetGuildVoiceChannelIDs(ctx context.Context, guildID string) ([]string, error) {
	if v.svc.redis == nil {
		return nil, nil
	}
	// Scan all voice:* keys and check if any participant belongs to this guild.
	var channelIDs []string
	seen := make(map[string]bool)
	iter := v.svc.redis.Scan(ctx, 0, "voice:*", 100).Iterator()
	for iter.Next(ctx) {
		key := iter.Val()
		chID := key[6:] // strip "voice:" prefix
		if seen[chID] {
			continue
		}
		states, err := v.svc.GetChannelState(ctx, chID)
		if err != nil || len(states) == 0 {
			continue
		}
		if states[0].GuildID == guildID {
			channelIDs = append(channelIDs, chID)
			seen[chID] = true
		}
	}
	return channelIDs, nil
}

// hasActiveAudio and hasActiveVideo are defined in webhook.go (same package).

// ServerMuteUser revokes/restores audio publish permission for a participant.
// LiveKit fires a track_unpublished webhook → the webhook handler emits a
// NATS event so every subscriber's UI updates automatically.
func (s *Service) ServerMuteUser(ctx context.Context, channelID, targetID string, muted bool) error {
	if s.roomClient == nil {
		return ErrLiveKitUnavailable
	}
	_, err := s.roomClient.UpdateParticipant(ctx, &livekit.UpdateParticipantRequest{
		Room:     channelID,
		Identity: targetID,
		Permission: &livekit.ParticipantPermission{
			CanPublish:   !muted,
			CanSubscribe: true,
		},
	})
	return err
}

// ServerDeafenUser revokes/restores subscribe permission so the target
// can't hear anyone. Also mutes them (can't publish while deaf).
func (s *Service) ServerDeafenUser(ctx context.Context, channelID, targetID string, deafened bool) error {
	if s.roomClient == nil {
		return ErrLiveKitUnavailable
	}
	_, err := s.roomClient.UpdateParticipant(ctx, &livekit.UpdateParticipantRequest{
		Room:     channelID,
		Identity: targetID,
		Permission: &livekit.ParticipantPermission{
			CanPublish:   !deafened,
			CanSubscribe: !deafened,
		},
	})
	return err
}

// DisconnectUser kicks a participant from the LiveKit room. LiveKit fires
// a participant_left webhook → NATS event propagates to all clients.
func (s *Service) DisconnectUser(ctx context.Context, channelID, targetID string) error {
	if s.roomClient == nil {
		return ErrLiveKitUnavailable
	}
	_, _ = s.roomClient.RemoveParticipant(ctx, &livekit.RoomParticipantIdentity{
		Room: channelID, Identity: targetID,
	})
	return nil
}

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
	payload := &streamv1.DmCallEvent{
		Type:      streamv1.DmCallType_DM_CALL_INCOMING,
		ChannelId: channelID,
		CallerId:  callerID,
		Video:     video,
		Timestamp: timestamppb.Now(),
	}
	for _, mid := range members {
		// _ = s.lpb.Publish(realtime.DmCall(mid), payload)
		realtime.Publish(s.lpb, realtime.DmCall(mid), payload)
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
	payload := &streamv1.DmCallEvent{
		Type:          streamv1.DmCallType_DM_CALL_ACCEPTED,
		ChannelId:     channelID,
		ParticipantId: userID,
		Video:         video,
		Timestamp:     timestamppb.Now(),
	}
	for _, mid := range members {
		// _ = s.lpb.Publish(realtime.DmCall(mid), payload)
		realtime.Publish(s.lpb, realtime.DmCall(mid), payload)
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
	payload := &streamv1.DmCallEvent{
		Type:          streamv1.DmCallType_DM_CALL_REJECTED,
		ChannelId:     channelID,
		ParticipantId: userID,
		Timestamp:     timestamppb.Now(),
	}
	for _, mid := range members {
		// _ = s.lpb.Publish(realtime.DmCall(mid), payload)
		realtime.Publish(s.lpb, realtime.DmCall(mid), payload)
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
	leftPayload := &streamv1.DmCallEvent{
		Type:          streamv1.DmCallType_DM_CALL_PARTICIPANT_LEFT,
		ChannelId:     channelID,
		ParticipantId: userID,
		Timestamp:     timestamppb.Now(),
	}
	for _, mid := range members {
		// _ = s.lpb.Publish(realtime.DmCall(mid), leftPayload)
		realtime.Publish(s.lpb, realtime.DmCall(mid), leftPayload)
	}

	// If the room is now empty, emit a terminal "call_ended" event so
	// recipients that were still ringing can dismiss their UI.
	remaining, err := s.roomClient.ListParticipants(ctx, &livekit.ListParticipantsRequest{
		Room: channelID,
	})
	if err == nil && len(remaining.Participants) == 0 {
		endPayload := &streamv1.DmCallEvent{
			Type:      streamv1.DmCallType_DM_CALL_ENDED,
			ChannelId: channelID,
			Timestamp: timestamppb.Now(),
		}
		for _, mid := range members {
			// _ = s.lpb.Publish(realtime.DmCall(mid), endPayload)
			realtime.Publish(s.lpb, realtime.DmCall(mid), endPayload)
		}
	}

	return nil
}
