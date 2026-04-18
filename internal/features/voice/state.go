package voice

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"

	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

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
	v, err := s.redis.Get(ctx, voiceActiveSinceKey(channelID)).Int64()
	if err != nil {
		return 0, err
	}
	return v, nil
}

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

func (s *Service) ClearActiveSince(ctx context.Context, channelID string) error {
	if s.redis == nil {
		return nil
	}
	return s.redis.Del(ctx, voiceActiveSinceKey(channelID)).Err()
}

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

func (s *Service) RemoveParticipant(ctx context.Context, channelID, userID string) error {
	if s.redis == nil {
		return nil
	}
	guildID := ""
	if ps, _ := s.GetParticipant(ctx, channelID, userID); ps != nil {
		guildID = ps.GuildID
	}

	if err := s.redis.HDel(ctx, voiceKey(channelID), userID).Err(); err != nil {
		return err
	}

	if guildID != "" && s.nats != nil {
		_ = s.nats.Publish(realtime.GuildChannelVoice(guildID, channelID),
			&streamv1.VoiceStateEvent{
				Event:           streamv1.VoiceEvent_VOICE_EVENT_STATE_UPDATE,
				Action:          streamv1.VoiceAction_VOICE_ACTION_LEAVE,
				ChannelId:       channelID,
				GuildId:         guildID,
				UserId:          userID,
				Timestamp:       timestamppb.Now(),
				RoomActiveSince: s.GetActiveSince(ctx, channelID),
			})
	}

	if n, err := s.redis.HLen(ctx, voiceKey(channelID)).Result(); err == nil && n == 0 {
		_ = s.redis.Del(ctx, voiceKey(channelID)).Err()
		_ = s.ClearActiveSince(ctx, channelID)
	}
	return nil
}

func (s *Service) ClearChannel(ctx context.Context, channelID string) error {
	if s.redis == nil {
		return nil
	}
	return s.redis.Del(ctx, voiceKey(channelID)).Err()
}

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

func (s *Service) toggleField(ctx context.Context, userID, channelID string, mutate func(*ParticipantState)) ([]ParticipantState, error) {
	ps, err := s.GetParticipant(ctx, channelID, userID)
	if err != nil || ps == nil {
		return nil, fmt.Errorf("not in voice channel")
	}
	mutate(ps)
	if err := s.SetParticipant(ctx, *ps); err != nil {
		return nil, err
	}

	s.broadcastParticipant(ctx, time.Now(), *ps)
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

func (s *Service) broadcastParticipant(ctx context.Context, activeSince time.Time, ps ParticipantState) {
	if ps.GuildID == "" {
		return
	}
	evt := &streamv1.VoiceStateEvent{
		Event:           streamv1.VoiceEvent_VOICE_EVENT_STATE_UPDATE,
		Action:          streamv1.VoiceAction_VOICE_ACTION_STATE_UPDATE,
		ChannelId:       ps.ChannelID,
		GuildId:         ps.GuildID,
		UserId:          ps.UserID,
		Name:            ps.DisplayName,
		SelfMute:        ps.SelfMute,
		SelfDeaf:        ps.SelfDeaf,
		Video:           ps.Video,
		Streaming:       ps.ScreenShare,
		Metadata:        fmt.Sprintf(`{"userId":"%s","username":"%s","displayName":"%s","avatarUrl":"%s"}`, ps.UserID, ps.Username, ps.DisplayName, ps.AvatarURL),
		Timestamp:       timestamppb.Now(),
		RoomActiveSince: s.GetActiveSince(ctx, ps.ChannelID),
	}
	_ = s.nats.Publish(realtime.GuildChannelVoice(ps.GuildID, ps.ChannelID), evt)
}

func (s *Service) broadcastChannelState(ctx context.Context, activeSince time.Time, channelID, guildID string) ([]ParticipantState, error) {
	states, err := s.GetChannelState(ctx, channelID)
	if err != nil {
		return nil, err
	}

	for _, p := range states {
		s.broadcastParticipant(ctx, activeSince, p)
	}
	return states, nil
}
