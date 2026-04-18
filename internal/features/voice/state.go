package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

// ParticipantState is stored per-user per-channel in Redis. Contains both
// the user profile (fetched from DB on join) and the real-time voice flags
// (updated on every mute/deaf/video/screen toggle).
type ParticipantState struct {
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
	GuildID     string `json:"guild_id"`
	ChannelID   string `json:"channel_id"`
	SelfMute    bool   `json:"self_mute"`
	SelfDeaf    bool   `json:"self_deaf"`
	Video       bool   `json:"video"`
	ScreenShare bool   `json:"screen_share"`
}

func voiceKey(channelID string) string {
	return "voice:" + channelID
}

func voiceActiveSinceKey(channelID string) string {
	return "voice:" + channelID + ":active_since"
}

// EnsureActiveSince sets the channel's active-since timestamp (unix seconds)
// if not already set. Called by the first joiner. Uses Redis SETNX semantics
// so concurrent joiners don't overwrite each other.
func (s *Service) EnsureActiveSince(ctx context.Context, channelID string) (int64, error) {
	if s.redis == nil {
		return 0, nil
	}
	now := time.Now().Unix()
	ok, err := s.redis.SetNX(ctx, voiceActiveSinceKey(channelID), now, 0).Result()
	if err != nil {
		return 0, err
	}
	if ok {
		return now, nil
	}
	// Someone else already set it — read the existing value.
	v, err := s.redis.Get(ctx, voiceActiveSinceKey(channelID)).Int64()
	if err != nil {
		return 0, err
	}
	return v, nil
}

// GetActiveSince returns the channel's active-since timestamp or 0 if empty.
func (s *Service) GetActiveSince(ctx context.Context, channelID string) int64 {
	if s.redis == nil {
		return 0
	}
	v, err := s.redis.Get(ctx, voiceActiveSinceKey(channelID)).Int64()
	if err != nil {
		return 0
	}
	return v
}

// ClearActiveSince deletes the channel's active-since timestamp. Called
// when the room is torn down (room_finished webhook or last leave).
func (s *Service) ClearActiveSince(ctx context.Context, channelID string) error {
	if s.redis == nil {
		return nil
	}
	return s.redis.Del(ctx, voiceActiveSinceKey(channelID)).Err()
}

// SetParticipant stores/updates a participant's state in the Redis hash
// for the given channel.
func (s *Service) SetParticipant(ctx context.Context, ps ParticipantState) error {
	if s.redis == nil {
		return nil
	}
	data, err := json.Marshal(ps)
	if err != nil {
		return err
	}
	return s.redis.HSet(ctx, voiceKey(ps.ChannelID), ps.UserID, data).Err()
}

// RemoveParticipant removes a user from the channel's Redis hash and
// broadcasts a "leave" event on the guild voice subject so every client
// can drop this user from their UI. If the channel becomes empty also
// clears the active_since marker so the next joiner starts a fresh timer.
func (s *Service) RemoveParticipant(ctx context.Context, channelID, userID string) error {
	if s.redis == nil {
		return nil
	}
	// Read guild_id BEFORE deleting so the leave event can be routed.
	guildID := ""
	if ps, _ := s.GetParticipant(ctx, channelID, userID); ps != nil {
		guildID = ps.GuildID
	}

	if err := s.redis.HDel(ctx, voiceKey(channelID), userID).Err(); err != nil {
		return err
	}

	// Explicit leave event so other clients drop this user from their UI.
	// Include room_active_since so late subscribers still get the correct
	// session start time for the channel.
	if guildID != "" && s.nats != nil {
		_ = s.nats.Publish(realtime.GuildChannelVoice(guildID, channelID), map[string]any{
			"event":             "VOICE_STATE_UPDATE",
			"action":            "leave",
			"channel_id":        channelID,
			"guild_id":          guildID,
			"user_id":           userID,
			"room_active_since": s.GetActiveSince(ctx, channelID),
		})
	}

	// If no one is left, clear everything.
	if n, err := s.redis.HLen(ctx, voiceKey(channelID)).Result(); err == nil && n == 0 {
		_ = s.redis.Del(ctx, voiceKey(channelID)).Err()
		_ = s.ClearActiveSince(ctx, channelID)
	}
	return nil
}

// ClearChannel removes all participants from a channel (room destroyed).
func (s *Service) ClearChannel(ctx context.Context, channelID string) error {
	if s.redis == nil {
		return nil
	}
	return s.redis.Del(ctx, voiceKey(channelID)).Err()
}

// GetParticipant loads a single participant's state.
func (s *Service) GetParticipant(ctx context.Context, channelID, userID string) (*ParticipantState, error) {
	if s.redis == nil {
		return nil, nil
	}
	data, err := s.redis.HGet(ctx, voiceKey(channelID), userID).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ps ParticipantState
	if err := json.Unmarshal([]byte(data), &ps); err != nil {
		return nil, err
	}
	return &ps, nil
}

// GetChannelState returns all participants in a channel from Redis.
func (s *Service) GetChannelState(ctx context.Context, channelID string) ([]ParticipantState, error) {
	if s.redis == nil {
		return nil, nil
	}
	all, err := s.redis.HGetAll(ctx, voiceKey(channelID)).Result()
	if err != nil {
		return nil, err
	}
	states := make([]ParticipantState, 0, len(all))
	for _, v := range all {
		var ps ParticipantState
		if json.Unmarshal([]byte(v), &ps) == nil {
			states = append(states, ps)
		}
	}
	return states, nil
}

// toggleField loads participant, applies a mutation, saves, broadcasts
// only the changed user's state — not the entire channel.
func (s *Service) toggleField(ctx context.Context, userID, channelID string, mutate func(*ParticipantState)) ([]ParticipantState, error) {
	ps, err := s.GetParticipant(ctx, channelID, userID)
	if err != nil || ps == nil {
		return nil, fmt.Errorf("not in voice channel")
	}
	mutate(ps)
	if err := s.SetParticipant(ctx, *ps); err != nil {
		return nil, err
	}
	// Broadcast only this user's updated state — other participants
	// are unchanged so no need to re-send them.
	s.broadcastParticipant(ctx, *ps)
	// Return full list for the RPC response.
	return s.GetChannelState(ctx, channelID)
}

func (s *Service) Mute(ctx context.Context, userID, channelID string) ([]ParticipantState, error) {
	return s.toggleField(ctx, userID, channelID, func(ps *ParticipantState) {
		ps.SelfMute = true
	})
}

func (s *Service) Unmute(ctx context.Context, userID, channelID string) ([]ParticipantState, error) {
	return s.toggleField(ctx, userID, channelID, func(ps *ParticipantState) {
		ps.SelfMute = false
	})
}

func (s *Service) Deafen(ctx context.Context, userID, channelID string) ([]ParticipantState, error) {
	return s.toggleField(ctx, userID, channelID, func(ps *ParticipantState) {
		ps.SelfDeaf = true
		ps.SelfMute = true
	})
}

func (s *Service) Undeafen(ctx context.Context, userID, channelID string) ([]ParticipantState, error) {
	return s.toggleField(ctx, userID, channelID, func(ps *ParticipantState) {
		ps.SelfDeaf = false
		ps.SelfMute = false
	})
}

func (s *Service) EnableVideo(ctx context.Context, userID, channelID string) ([]ParticipantState, error) {
	return s.toggleField(ctx, userID, channelID, func(ps *ParticipantState) {
		ps.Video = true
	})
}

func (s *Service) DisableVideo(ctx context.Context, userID, channelID string) ([]ParticipantState, error) {
	return s.toggleField(ctx, userID, channelID, func(ps *ParticipantState) {
		ps.Video = false
	})
}

func (s *Service) StartScreenShare(ctx context.Context, userID, channelID string) ([]ParticipantState, error) {
	return s.toggleField(ctx, userID, channelID, func(ps *ParticipantState) {
		ps.ScreenShare = true
	})
}

func (s *Service) StopScreenShare(ctx context.Context, userID, channelID string) ([]ParticipantState, error) {
	return s.toggleField(ctx, userID, channelID, func(ps *ParticipantState) {
		ps.ScreenShare = false
	})
}

// broadcastParticipant publishes the specific user's voice state as an
// event that maps 1:1 to VoiceStateEvent proto fields. Called after every
// toggle so StreamVoiceState subscribers get a proper typed event.
func (s *Service) broadcastParticipant(ctx context.Context, ps ParticipantState) {
	if ps.GuildID == "" {
		return
	}
	payload := map[string]any{
		"event":      "VOICE_STATE_UPDATE",
		"action":     "state_update",
		"channel_id": ps.ChannelID,
		"guild_id":   ps.GuildID,
		"user_id":    ps.UserID,
		"name":       ps.DisplayName,
		"self_mute":  ps.SelfMute,
		"self_deaf":  ps.SelfDeaf,
		"video":      ps.Video,
		"streaming":  ps.ScreenShare,
		"metadata":   fmt.Sprintf(`{"userId":"%s","username":"%s","displayName":"%s","avatarUrl":"%s"}`, ps.UserID, ps.Username, ps.DisplayName, ps.AvatarURL),
	}
	// Attach room_active_since from Redis (set on first joiner).
	if active := s.GetActiveSince(context.Background(), ps.ChannelID); active > 0 {
		payload["room_active_since"] = active
	}
	_ = s.nats.Publish(realtime.GuildChannelVoice(ps.GuildID, ps.ChannelID), payload)
}

// broadcastChannelState publishes every participant's state individually.
// Used on join/leave to sync the full channel.
func (s *Service) broadcastChannelState(ctx context.Context, channelID, guildID string) ([]ParticipantState, error) {
	states, err := s.GetChannelState(ctx, channelID)
	if err != nil {
		return nil, err
	}
	for _, p := range states {
		s.broadcastParticipant(ctx, p)
	}
	return states, nil
}
