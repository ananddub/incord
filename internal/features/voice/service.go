package voice

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

const (
	voiceChannelKeyPrefix = "voice:"
	voiceStateKeyPrefix   = "voicestate:"
	voiceStateTTL         = 24 * time.Hour
)

var (
	ErrInsufficientPermissions = errors.New("insufficient permissions")
)

// Service contains the business logic for the voice feature.
type Service struct {
	redis    *redis.Client
	voiceCfg config.VoiceConfig
	authz    *authz.Client
	nats     *realtime.Hub
}

// NewService creates a new voice Service.
func NewService(rdb *redis.Client, voiceCfg config.VoiceConfig, nats *realtime.Hub, authzClient ...*authz.Client) *Service {
	s := &Service{
		redis:    rdb,
		voiceCfg: voiceCfg,
		nats:     nats,
	}
	if len(authzClient) > 0 {
		s.authz = authzClient[0]
	}
	return s
}

// JoinChannelResult holds the data returned when a user joins a voice channel.
type JoinChannelResult struct {
	SessionID     string
	UDPEndpoint   string
	UDPPort       int32
	EncryptionKey []byte
	SSRC          uint32
}

// JoinChannel adds a user as a participant in a voice channel.
func (s *Service) JoinChannel(ctx context.Context, userID, guildID, channelID string) (*JoinChannelResult, error) {
	// Use CanViewChannel as proxy for voice connect permission
	if guildID != "" && !s.authz.CanViewChannel(ctx, userID, channelID) {
		return nil, ErrInsufficientPermissions
	}

	sessionID := uuid.New().String()

	// Generate a random SSRC
	ssrc, err := generateSSRC()
	if err != nil {
		return nil, fmt.Errorf("failed to generate SSRC: %w", err)
	}

	// Generate a random encryption key (32 bytes)
	encryptionKey := make([]byte, 32)
	if _, err := rand.Read(encryptionKey); err != nil {
		return nil, fmt.Errorf("failed to generate encryption key: %w", err)
	}

	// Add participant to the channel's Redis set
	channelKey := voiceChannelKeyPrefix + channelID
	if err := s.redis.SAdd(ctx, channelKey, sessionID).Err(); err != nil {
		return nil, fmt.Errorf("failed to add participant to channel: %w", err)
	}

	// Store voice state as a Redis hash
	stateKey := voiceStateKeyPrefix + sessionID
	fields := map[string]interface{}{
		"user_id":       userID,
		"guild_id":      guildID,
		"channel_id":    channelID,
		"ssrc":          ssrc,
		"muted":         false,
		"deafened":      false,
		"self_muted":    false,
		"self_deafened": false,
		"streaming":     false,
		"video":         false,
	}
	if err := s.redis.HSet(ctx, stateKey, fields).Err(); err != nil {
		return nil, fmt.Errorf("failed to store voice state: %w", err)
	}
	s.redis.Expire(ctx, stateKey, voiceStateTTL)

	// Publish voice state event
	_ = s.nats.Publish(realtime.GuildChannelVoice(guildID, channelID), map[string]any{
		"event":      "VOICE_STATE_UPDATE",
		"action":     "join",
		"user_id":    userID,
		"channel_id": channelID,
		"session_id": sessionID,
	})

	return &JoinChannelResult{
		SessionID:     sessionID,
		UDPEndpoint:   s.voiceCfg.UDPHost,
		UDPPort:       int32(s.voiceCfg.UDPPort),
		EncryptionKey: encryptionKey,
		SSRC:          ssrc,
	}, nil
}

// LeaveChannel removes a user from a voice channel.
func (s *Service) LeaveChannel(ctx context.Context, userID, channelID string) error {
	// Find the session for this user in this channel
	channelKey := voiceChannelKeyPrefix + channelID
	sessionIDs, err := s.redis.SMembers(ctx, channelKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get channel participants: %w", err)
	}

	for _, sid := range sessionIDs {
		stateKey := voiceStateKeyPrefix + sid
		storedUserID, err := s.redis.HGet(ctx, stateKey, "user_id").Result()
		if err != nil {
			continue
		}
		if storedUserID == userID {
			// Read guild_id before deleting state
			guildID, _ := s.redis.HGet(ctx, stateKey, "guild_id").Result()

			// Remove from channel set and delete state
			s.redis.SRem(ctx, channelKey, sid)
			s.redis.Del(ctx, stateKey)

			// Publish voice state event
			_ = s.nats.Publish(realtime.GuildChannelVoice(guildID, channelID), map[string]any{
				"event":      "VOICE_STATE_UPDATE",
				"action":     "leave",
				"user_id":    userID,
				"channel_id": channelID,
				"session_id": sid,
			})
			break
		}
	}

	return nil
}

// GetChannelParticipants returns all participants in a voice channel.
func (s *Service) GetChannelParticipants(ctx context.Context, channelID string) ([]*voicev1.VoiceParticipant, error) {
	channelKey := voiceChannelKeyPrefix + channelID
	sessionIDs, err := s.redis.SMembers(ctx, channelKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get channel participants: %w", err)
	}

	participants := make([]*voicev1.VoiceParticipant, 0, len(sessionIDs))

	for _, sid := range sessionIDs {
		stateKey := voiceStateKeyPrefix + sid
		vals, err := s.redis.HGetAll(ctx, stateKey).Result()
		if err != nil || len(vals) == 0 {
			// Stale session, clean up
			s.redis.SRem(ctx, channelKey, sid)
			continue
		}

		p := &voicev1.VoiceParticipant{
			UserId:    vals["user_id"],
			SessionId: sid,
		}

		if ssrcStr, ok := vals["ssrc"]; ok {
			if v, convErr := strconv.ParseUint(ssrcStr, 10, 32); convErr == nil {
				p.Ssrc = uint32(v)
			}
		}
		p.Muted = vals["muted"] == "1"
		p.Deafened = vals["deafened"] == "1"
		p.SelfMuted = vals["self_muted"] == "1"
		p.SelfDeafened = vals["self_deafened"] == "1"
		p.Streaming = vals["streaming"] == "1"
		p.Video = vals["video"] == "1"

		participants = append(participants, p)
	}

	return participants, nil
}

// UpdateVoiceState updates the voice state for a user in a channel.
func (s *Service) UpdateVoiceState(ctx context.Context, userID, channelID string, selfMute, selfDeaf, video, stream bool) error {
	channelKey := voiceChannelKeyPrefix + channelID
	sessionIDs, err := s.redis.SMembers(ctx, channelKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get channel participants: %w", err)
	}

	for _, sid := range sessionIDs {
		stateKey := voiceStateKeyPrefix + sid
		storedUserID, err := s.redis.HGet(ctx, stateKey, "user_id").Result()
		if err != nil {
			continue
		}
		if storedUserID == userID {
			fields := map[string]interface{}{
				"self_muted":    selfMute,
				"self_deafened": selfDeaf,
				"video":         video,
				"streaming":     stream,
			}
			if err := s.redis.HSet(ctx, stateKey, fields).Err(); err != nil {
				return fmt.Errorf("failed to update voice state: %w", err)
			}

			// Read guild_id for subject routing
			guildID, _ := s.redis.HGet(ctx, stateKey, "guild_id").Result()

			// Publish update event
			_ = s.nats.Publish(realtime.GuildChannelVoice(guildID, channelID), map[string]any{
				"event":         "VOICE_STATE_UPDATE",
				"action":        "update",
				"user_id":       userID,
				"channel_id":    channelID,
				"session_id":    sid,
				"self_muted":    selfMute,
				"self_deafened": selfDeaf,
				"video":         video,
				"streaming":     stream,
			})
			return nil
		}
	}

	return fmt.Errorf("user not found in channel")
}

// generateSSRC returns a random 32-bit SSRC value.
func generateSSRC() (uint32, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1<<32-1))
	if err != nil {
		return 0, err
	}
	return uint32(n.Uint64()), nil
}
