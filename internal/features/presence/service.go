package presence

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/types/known/timestamppb"

	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

const presenceKeyPrefix = "presence:"

// UserInfoResolver returns the basic profile of a user (username + avatar URL)
// so presence events can be enriched without a hard dependency on the user
// feature package.
type UserInfoResolver interface {
	LookupBasicProfile(ctx context.Context, userID string) (username, avatarURL string)
}

// Service contains the business logic for the presence feature.
type Service struct {
	redis    *redis.Client
	nats     *realtime.Hub
	resolver UserInfoResolver
}

// NewService creates a new presence Service.
func NewService(rdb *redis.Client, nats *realtime.Hub) *Service {
	return &Service{
		redis: rdb,
		nats:  nats,
	}
}

// SetUserResolver wires a user-info resolver used to enrich presence events.
func (s *Service) SetUserResolver(r UserInfoResolver) {
	s.resolver = r
}

// statusToString maps the presence enum to the string used for the
// hash-stored column (Redis persistence, not the wire).
var statusToString = map[presencev1.Status]string{
	presencev1.Status_STATUS_ONLINE:  "online",
	presencev1.Status_STATUS_IDLE:    "idle",
	presencev1.Status_STATUS_DND:     "dnd",
	presencev1.Status_STATUS_OFFLINE: "offline",
}

// statusToStream maps the presence enum to the stream proto enum.
var statusToStream = map[presencev1.Status]streamv1.PresenceStatus{
	presencev1.Status_STATUS_ONLINE:  streamv1.PresenceStatus_PRESENCE_STATUS_ONLINE,
	presencev1.Status_STATUS_IDLE:    streamv1.PresenceStatus_PRESENCE_STATUS_IDLE,
	presencev1.Status_STATUS_DND:     streamv1.PresenceStatus_PRESENCE_STATUS_DND,
	presencev1.Status_STATUS_OFFLINE: streamv1.PresenceStatus_PRESENCE_STATUS_OFFLINE,
}

// publishPresence fans out a presence_update event on the user's FriendActivity
// subject. StreamFriendActivity subscribers on this subject (the user's
// friends) will receive it.
func (s *Service) publishPresence(ctx context.Context, userID string, status presencev1.Status, customStatus string) {
	if s.nats == nil {
		return
	}
	payload := &streamv1.FriendActivityEvent{
		Event:        streamv1.FriendEventType_FRIEND_EVENT_PRESENCE_UPDATE,
		UserId:       userID,
		Status:       statusToStream[status],
		CustomStatus: customStatus,
		Timestamp:    timestamppb.Now(),
	}
	if s.resolver != nil {
		username, avatarURL := s.resolver.LookupBasicProfile(ctx, userID)
		payload.Username = username
		payload.AvatarUrl = avatarURL
	}
	_ = s.nats.Publish(realtime.FriendActivity(userID), payload)
}

// UpdatePresence sets the presence for a user in Redis and broadcasts the
// event on the user's FriendActivity subject.
func (s *Service) UpdatePresence(ctx context.Context, userID string, st presencev1.Status, customStatus string) (*presencev1.Presence, error) {
	key := presenceKeyPrefix + userID
	now := time.Now()

	fields := map[string]interface{}{
		"status":        statusToString[st],
		"custom_status": customStatus,
		"last_seen":     now.Unix(),
	}

	if err := s.redis.HSet(ctx, key, fields).Err(); err != nil {
		return nil, fmt.Errorf("failed to set presence: %w", err)
	}

	s.publishPresence(ctx, userID, st, customStatus)

	p := &presencev1.Presence{
		UserId:       userID,
		Status:       st,
		CustomStatus: customStatus,
		LastSeen:     timestamppb.New(now),
	}

	return p, nil
}

// SetOffline sets the user's presence to OFFLINE and publishes the event.
func (s *Service) SetOffline(ctx context.Context, userID string) error {
	_, err := s.UpdatePresence(ctx, userID, presencev1.Status_STATUS_OFFLINE, "")
	return err
}

// GetPresence retrieves the presence for a single user from Redis.
func (s *Service) GetPresence(ctx context.Context, userID string) (*presencev1.Presence, error) {
	key := presenceKeyPrefix + userID

	vals, err := s.redis.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get presence: %w", err)
	}

	// If no data found, return offline status
	if len(vals) == 0 {
		return &presencev1.Presence{
			UserId: userID,
			Status: presencev1.Status_STATUS_OFFLINE,
		}, nil
	}

	return parsePresence(userID, vals), nil
}

// GetBulkPresence retrieves presence for multiple users.
func (s *Service) GetBulkPresence(ctx context.Context, userIDs []string) ([]*presencev1.Presence, error) {
	presences := make([]*presencev1.Presence, 0, len(userIDs))

	pipe := s.redis.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(userIDs))

	for i, uid := range userIDs {
		cmds[i] = pipe.HGetAll(ctx, presenceKeyPrefix+uid)
	}

	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, fmt.Errorf("failed to get bulk presence: %w", err)
	}

	for i, uid := range userIDs {
		vals, err := cmds[i].Result()
		if err != nil || len(vals) == 0 {
			presences = append(presences, &presencev1.Presence{
				UserId: uid,
				Status: presencev1.Status_STATUS_OFFLINE,
			})
			continue
		}
		presences = append(presences, parsePresence(uid, vals))
	}

	return presences, nil
}

// parsePresence converts a Redis hash map into a Presence proto message.
func parsePresence(userID string, vals map[string]string) *presencev1.Presence {
	p := &presencev1.Presence{
		UserId: userID,
	}

	if statusVal, ok := vals["status"]; ok {
		strToStatus := map[string]presencev1.Status{
			"online":  presencev1.Status_STATUS_ONLINE,
			"idle":    presencev1.Status_STATUS_IDLE,
			"dnd":     presencev1.Status_STATUS_DND,
			"offline": presencev1.Status_STATUS_OFFLINE,
		}
		if st, found := strToStatus[statusVal]; found {
			p.Status = st
		} else if v, err := strconv.ParseInt(statusVal, 10, 32); err == nil {
			p.Status = presencev1.Status(v) // backwards compat with old int format
		}
	}

	if cs, ok := vals["custom_status"]; ok {
		p.CustomStatus = cs
	}

	if lsStr, ok := vals["last_seen"]; ok {
		if ts, err := strconv.ParseInt(lsStr, 10, 64); err == nil {
			p.LastSeen = timestamppb.New(time.Unix(ts, 0))
		}
	}

	return p
}
