package gateway

import (
	"context"
	"encoding/json"

	"google.golang.org/protobuf/types/known/timestamppb"

	gatewayv1 "github.com/ananddub/ndiscord_backend/gen/gateway/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
	"github.com/ananddub/ndiscord_backend/internal/shared/event"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// FriendLookup resolves a user's friend list for event routing.
type FriendLookup interface {
	GetFriendIDs(ctx context.Context, userID string) ([]string, error)
}

// Dispatcher consumes events from Redpanda and pushes them to:
// 1. Legacy gateway sessions (bidirectional stream)
// 2. Per-feature subscription streams (dedicated per-resource streams)
// Events are filtered by permissions - users only get events they're allowed to see.
type Dispatcher struct {
	sessions     *SessionManager
	consumer     *event.Consumer
	friendLookup FriendLookup
	authz        *authz.Client
	MsgSubs      *event.SubscriptionManager[*messagev1.MessageEvent]
	TypingSubs   *event.SubscriptionManager[*messagev1.TypingEvent]
	GuildSubs    *event.SubscriptionManager[*guildv1.GuildEvent]
	FriendSubs   *event.SubscriptionManager[*userv1.FriendActivityEvent]
	VoiceSubs    *event.SubscriptionManager[*voicev1.VoiceStateEvent]
}

func NewDispatcher(sessions *SessionManager, consumer *event.Consumer, friendLookup FriendLookup, authzClient *authz.Client) *Dispatcher {
	return &Dispatcher{
		sessions:     sessions,
		consumer:     consumer,
		friendLookup: friendLookup,
		authz:        authzClient,
		MsgSubs:      event.NewSubscriptionManager[*messagev1.MessageEvent](),
		TypingSubs:   event.NewSubscriptionManager[*messagev1.TypingEvent](),
		GuildSubs:    event.NewSubscriptionManager[*guildv1.GuildEvent](),
		FriendSubs:   event.NewSubscriptionManager[*userv1.FriendActivityEvent](),
		VoiceSubs:    event.NewSubscriptionManager[*voicev1.VoiceStateEvent](),
	}
}

// Start begins consuming events from Redpanda and dispatching to clients.
func (d *Dispatcher) Start(ctx context.Context) error {
	return d.consumer.Start(ctx, d.handleEvent)
}

// broadcastToGuildFiltered sends event only to guild members who have ViewChannels permission.
// For guild-level events (no channelID), it sends to all members.
func (d *Dispatcher) broadcastToGuildFiltered(guildID, channelID string, evt *gatewayv1.ServerEvent) {
	if channelID == "" || d.authz == nil {
		// No channel = guild-level event, send to all
		d.sessions.BroadcastToGuild(guildID, evt)
		return
	}

	// Channel-specific event - filter by permission
	d.sessions.mu.RLock()
	members, ok := d.sessions.guildMembers[guildID]
	if !ok {
		d.sessions.mu.RUnlock()
		return
	}

	// Collect eligible user IDs
	var eligibleUsers []string
	for userID := range members {
		eligibleUsers = append(eligibleUsers, userID)
	}
	d.sessions.mu.RUnlock()

	for _, userID := range eligibleUsers {
		if d.authz.CanViewChannel(context.Background(), userID, channelID) {
			d.sessions.mu.RLock()
			userMap, exists := d.sessions.userSessions[userID]
			if exists {
				for _, s := range userMap {
					select {
					case s.Send <- evt:
					default:
					}
				}
			}
			d.sessions.mu.RUnlock()
		}
	}
}

func (d *Dispatcher) handleEvent(ctx context.Context, key, value []byte) error {
	var envelope event.Envelope
	if err := json.Unmarshal(value, &envelope); err != nil {
		logger.Log.Error().Err(err).Msg("failed to unmarshal event envelope")
		return nil // don't retry malformed events
	}

	switch envelope.Type {
	case event.TopicMessageCreate:
		return d.handleMessageCreate(envelope)
	case event.TopicMessageUpdate:
		return d.handleMessageUpdate(envelope)
	case event.TopicMessageDelete:
		return d.handleMessageDelete(envelope)
	case event.TopicTyping:
		return d.handleTypingStart(envelope)
	case event.TopicPresence:
		return d.handlePresenceUpdate(envelope)
	case event.TopicGuildEvents:
		return d.handleGuildEvent(envelope)
	case event.TopicChannelEvents:
		return d.handleChannelEvent(envelope)
	case event.TopicVoiceState:
		return d.handleVoiceStateUpdate(envelope)
	case event.TopicUserUpdate:
		return d.handleUserUpdate(envelope)
	default:
		logger.Log.Warn().Str("type", envelope.Type).Msg("unknown event type")
	}
	return nil
}

// MessageEventData is the data payload for message events.
type MessageEventData struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	AuthorID  string `json:"author_id"`
	Content   string `json:"content"`
}

func (d *Dispatcher) handleMessageCreate(env event.Envelope) error {
	var data MessageEventData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	evt := &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_MESSAGE_CREATE,
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_MessageCreate{
			MessageCreate: &gatewayv1.MessageCreate{
				Id:        data.ID,
				ChannelId: data.ChannelID,
				GuildId:   data.GuildID,
				AuthorId:  data.AuthorID,
				Content:   data.Content,
				Timestamp: timestamppb.Now(),
			},
		},
	}

	if data.GuildID != "" {
		d.broadcastToGuildFiltered(data.GuildID, data.ChannelID, evt)
	}

	// Push to per-channel message stream subscribers
	d.MsgSubs.Publish(data.ChannelID, &messagev1.MessageEvent{
		Type:      messagev1.MessageEventType_MESSAGE_EVENT_TYPE_CREATE,
		ChannelId: data.ChannelID,
		MessageId: data.ID,
		UserId:    data.AuthorID,
		Timestamp: timestamppb.Now(),
	})

	return nil
}

func (d *Dispatcher) handleMessageUpdate(env event.Envelope) error {
	var data MessageEventData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	evt := &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_MESSAGE_UPDATE,
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_MessageUpdate{
			MessageUpdate: &gatewayv1.MessageUpdate{
				Id:        data.ID,
				ChannelId: data.ChannelID,
				GuildId:   data.GuildID,
				Content:   data.Content,
				EditedAt:  timestamppb.Now(),
			},
		},
	}

	if data.GuildID != "" {
		d.broadcastToGuildFiltered(data.GuildID, data.ChannelID, evt)
	}
	d.MsgSubs.Publish(data.ChannelID, &messagev1.MessageEvent{
		Type: messagev1.MessageEventType_MESSAGE_EVENT_TYPE_UPDATE, ChannelId: data.ChannelID, MessageId: data.ID, Timestamp: timestamppb.Now(),
	})
	return nil
}

func (d *Dispatcher) handleMessageDelete(env event.Envelope) error {
	var data MessageEventData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	evt := &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_MESSAGE_DELETE,
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_MessageDelete{
			MessageDelete: &gatewayv1.MessageDelete{
				Id:        data.ID,
				ChannelId: data.ChannelID,
				GuildId:   data.GuildID,
			},
		},
	}

	if data.GuildID != "" {
		d.broadcastToGuildFiltered(data.GuildID, data.ChannelID, evt)
	}
	d.MsgSubs.Publish(data.ChannelID, &messagev1.MessageEvent{
		Type: messagev1.MessageEventType_MESSAGE_EVENT_TYPE_DELETE, ChannelId: data.ChannelID, MessageId: data.ID, Timestamp: timestamppb.Now(),
	})
	return nil
}

// TypingEventData is the data for typing events.
type TypingEventData struct {
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	UserID    string `json:"user_id"`
}

func (d *Dispatcher) handleTypingStart(env event.Envelope) error {
	var data TypingEventData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	evt := &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_TYPING_START,
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_TypingStart{
			TypingStart: &gatewayv1.TypingStart{
				ChannelId: data.ChannelID,
				GuildId:   data.GuildID,
				UserId:    data.UserID,
				Timestamp: timestamppb.Now(),
			},
		},
	}

	if data.GuildID != "" {
		d.broadcastToGuildFiltered(data.GuildID, data.ChannelID, evt)
	}
	d.TypingSubs.Publish(data.ChannelID, &messagev1.TypingEvent{
		ChannelId: data.ChannelID, UserId: data.UserID, Timestamp: timestamppb.Now(),
	})
	return nil
}

// PresenceEventData is the data for presence events.
type PresenceEventData struct {
	UserID       string `json:"user_id"`
	Status       string `json:"status"`
	CustomStatus string `json:"custom_status"`
}

func (d *Dispatcher) handlePresenceUpdate(env event.Envelope) error {
	var data PresenceEventData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	statusMap := map[string]gatewayv1.PresenceStatus{
		"online":  gatewayv1.PresenceStatus_PRESENCE_STATUS_ONLINE,
		"idle":    gatewayv1.PresenceStatus_PRESENCE_STATUS_IDLE,
		"dnd":     gatewayv1.PresenceStatus_PRESENCE_STATUS_DND,
		"offline": gatewayv1.PresenceStatus_PRESENCE_STATUS_OFFLINE,
	}

	evt := &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_PRESENCE_UPDATE,
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_PresenceUpdate{
			PresenceUpdate: &gatewayv1.PresenceUpdate{
				UserId:       data.UserID,
				Status:       statusMap[data.Status],
				CustomStatus: data.CustomStatus,
			},
		},
	}

	// Broadcast to all sessions of this user's friends/guilds
	// For now, broadcast to all sessions that share a guild with this user
	userSessions := d.sessions.GetUserSessions(data.UserID)
	for _, session := range userSessions {
		for _, guildID := range session.GuildIDs {
			d.sessions.BroadcastToGuild(guildID, evt)
		}
	}
	// Push presence update to ALL friends of this user
	friendEvt := &userv1.FriendActivityEvent{
		Type:         userv1.FriendActivityType_FRIEND_ACTIVITY_TYPE_PRESENCE_UPDATE,
		UserId:       data.UserID,
		Status:       data.Status,
		CustomStatus: data.CustomStatus,
		Timestamp:    timestamppb.Now(),
	}

	if d.friendLookup != nil {
		friendIDs, lookupErr := d.friendLookup.GetFriendIDs(context.Background(), data.UserID)
		if lookupErr != nil {
			logger.Log.Error().Err(lookupErr).Str("user_id", data.UserID).Msg("failed to lookup friends for presence")
		} else {
			for _, friendID := range friendIDs {
				d.FriendSubs.Publish(friendID, friendEvt)
			}
		}
	}
	return nil
}

// UserUpdateData is the data for user profile update events.
type UserUpdateData struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatar_url"`
	Bio       string `json:"bio"`
	Status    string `json:"status"`
}

func (d *Dispatcher) handleUserUpdate(env event.Envelope) error {
	var data UserUpdateData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	// Notify all friends about profile change
	profileEvt := &userv1.FriendActivityEvent{
		Type:   userv1.FriendActivityType_FRIEND_ACTIVITY_TYPE_PROFILE_UPDATE,
		UserId: data.UserID,
		User: &userv1.User{
			Id:        data.UserID,
			Username:  data.Username,
			AvatarUrl: data.AvatarURL,
			Bio:       data.Bio,
		},
		Timestamp: timestamppb.Now(),
	}

	if d.friendLookup != nil {
		friendIDs, err := d.friendLookup.GetFriendIDs(context.Background(), data.UserID)
		if err != nil {
			logger.Log.Error().Err(err).Str("user_id", data.UserID).Msg("failed to lookup friends for profile update")
		} else {
			for _, friendID := range friendIDs {
				d.FriendSubs.Publish(friendID, profileEvt)
			}
		}
	}

	// Also broadcast to all guilds this user is in via gateway sessions
	userSessions := d.sessions.GetUserSessions(data.UserID)
	if len(userSessions) > 0 {
		guildEvt := &gatewayv1.ServerEvent{
			Type:      gatewayv1.EventType_EVENT_TYPE_PRESENCE_UPDATE,
			Timestamp: timestamppb.Now(),
			Data: &gatewayv1.ServerEvent_PresenceUpdate{
				PresenceUpdate: &gatewayv1.PresenceUpdate{
					UserId: data.UserID,
				},
			},
		}
		for _, session := range userSessions {
			for _, guildID := range session.GuildIDs {
				d.sessions.BroadcastToGuild(guildID, guildEvt)
			}
		}
	}

	return nil
}

// GuildEventData is the data for guild events.
type GuildEventData struct {
	Action  string `json:"action"` // "create", "update", "delete", "member_add", "member_remove"
	GuildID string `json:"guild_id"`
	Name    string `json:"name,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
	OwnerID string `json:"owner_id,omitempty"`
	UserID  string `json:"user_id,omitempty"`
}

func (d *Dispatcher) handleGuildEvent(env event.Envelope) error {
	var data GuildEventData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	var evt *gatewayv1.ServerEvent
	switch data.Action {
	case "member_add":
		evt = &gatewayv1.ServerEvent{
			Type:      gatewayv1.EventType_EVENT_TYPE_GUILD_MEMBER_ADD,
			Timestamp: timestamppb.Now(),
			Data: &gatewayv1.ServerEvent_GuildMemberAdd{
				GuildMemberAdd: &gatewayv1.GuildMemberAdd{
					GuildId: data.GuildID,
					UserId:  data.UserID,
				},
			},
		}
	case "member_remove":
		evt = &gatewayv1.ServerEvent{
			Type:      gatewayv1.EventType_EVENT_TYPE_GUILD_MEMBER_REMOVE,
			Timestamp: timestamppb.Now(),
			Data: &gatewayv1.ServerEvent_GuildMemberRemove{
				GuildMemberRemove: &gatewayv1.GuildMemberRemove{
					GuildId: data.GuildID,
					UserId:  data.UserID,
				},
			},
		}
	case "update":
		evt = &gatewayv1.ServerEvent{
			Type:      gatewayv1.EventType_EVENT_TYPE_GUILD_UPDATE,
			Timestamp: timestamppb.Now(),
			Data: &gatewayv1.ServerEvent_GuildUpdate{
				GuildUpdate: &gatewayv1.GuildUpdate{
					Id:      data.GuildID,
					Name:    data.Name,
					IconUrl: data.IconURL,
				},
			},
		}
	case "delete":
		evt = &gatewayv1.ServerEvent{
			Type:      gatewayv1.EventType_EVENT_TYPE_GUILD_DELETE,
			Timestamp: timestamppb.Now(),
			Data: &gatewayv1.ServerEvent_GuildDelete{
				GuildDelete: &gatewayv1.GuildDelete{
					Id: data.GuildID,
				},
			},
		}
	default:
		return nil
	}

	// Guild events go to ALL members (no channel filter)
	d.broadcastToGuildFiltered(data.GuildID, "", evt)

	// Push to per-guild stream subscribers
	guildEventTypeMap := map[string]guildv1.GuildEventType{
		"member_add":    guildv1.GuildEventType_GUILD_EVENT_TYPE_MEMBER_ADD,
		"member_remove": guildv1.GuildEventType_GUILD_EVENT_TYPE_MEMBER_REMOVE,
		"update":        guildv1.GuildEventType_GUILD_EVENT_TYPE_UPDATED,
		"delete":        guildv1.GuildEventType_GUILD_EVENT_TYPE_DELETED,
	}
	if t, ok := guildEventTypeMap[data.Action]; ok {
		d.GuildSubs.Publish(data.GuildID, &guildv1.GuildEvent{
			Type: t, GuildId: data.GuildID, UserId: data.UserID, Timestamp: timestamppb.Now(),
		})
	}
	return nil
}

// ChannelEventData is the data for channel events.
type ChannelEventData struct {
	Action    string `json:"action"` // "create", "update", "delete"
	ChannelID string `json:"channel_id"`
	GuildID   string `json:"guild_id"`
	Name      string `json:"name,omitempty"`
	Topic     string `json:"topic,omitempty"`
	Type      int32  `json:"type,omitempty"`
}

func (d *Dispatcher) handleChannelEvent(env event.Envelope) error {
	var data ChannelEventData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	var evt *gatewayv1.ServerEvent
	switch data.Action {
	case "create":
		evt = &gatewayv1.ServerEvent{
			Type:      gatewayv1.EventType_EVENT_TYPE_CHANNEL_CREATE,
			Timestamp: timestamppb.Now(),
			Data: &gatewayv1.ServerEvent_ChannelCreate{
				ChannelCreate: &gatewayv1.ChannelCreate{
					Id:      data.ChannelID,
					GuildId: data.GuildID,
					Name:    data.Name,
					Type:    data.Type,
				},
			},
		}
	case "update":
		evt = &gatewayv1.ServerEvent{
			Type:      gatewayv1.EventType_EVENT_TYPE_CHANNEL_UPDATE,
			Timestamp: timestamppb.Now(),
			Data: &gatewayv1.ServerEvent_ChannelUpdate{
				ChannelUpdate: &gatewayv1.ChannelUpdate{
					Id:      data.ChannelID,
					GuildId: data.GuildID,
					Name:    data.Name,
					Topic:   data.Topic,
				},
			},
		}
	case "delete":
		evt = &gatewayv1.ServerEvent{
			Type:      gatewayv1.EventType_EVENT_TYPE_CHANNEL_DELETE,
			Timestamp: timestamppb.Now(),
			Data: &gatewayv1.ServerEvent_ChannelDelete{
				ChannelDelete: &gatewayv1.ChannelDelete{
					Id:      data.ChannelID,
					GuildId: data.GuildID,
				},
			},
		}
	default:
		return nil
	}

	if data.GuildID != "" {
		d.broadcastToGuildFiltered(data.GuildID, data.ChannelID, evt)
	}
	return nil
}

// VoiceStateEventData is the data for voice state events.
type VoiceStateEventData struct {
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	UserID    string `json:"user_id"`
	SessionID string `json:"session_id"`
	SelfMute  bool   `json:"self_mute"`
	SelfDeaf  bool   `json:"self_deaf"`
}

func (d *Dispatcher) handleVoiceStateUpdate(env event.Envelope) error {
	var data VoiceStateEventData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil
	}

	evt := &gatewayv1.ServerEvent{
		Type:      gatewayv1.EventType_EVENT_TYPE_VOICE_STATE_UPDATE,
		Timestamp: timestamppb.Now(),
		Data: &gatewayv1.ServerEvent_VoiceStateUpdate{
			VoiceStateUpdate: &gatewayv1.VoiceStateUpdate{
				GuildId:   data.GuildID,
				ChannelId: data.ChannelID,
				UserId:    data.UserID,
				SessionId: data.SessionID,
				SelfMute:  data.SelfMute,
				SelfDeaf:  data.SelfDeaf,
			},
		},
	}

	if data.GuildID != "" {
		d.broadcastToGuildFiltered(data.GuildID, data.ChannelID, evt)
	}

	d.VoiceSubs.Publish(data.ChannelID, &voicev1.VoiceStateEvent{
		Type:      voicev1.VoiceStateEventType_VOICE_STATE_EVENT_TYPE_UPDATE,
		ChannelId: data.ChannelID,
		Participant: &voicev1.VoiceParticipant{
			UserId:       data.UserID,
			SessionId:    data.SessionID,
			SelfMuted:    data.SelfMute,
			SelfDeafened: data.SelfDeaf,
		},
		Timestamp: timestamppb.Now(),
	})
	return nil
}
