package realtime

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// Hub wraps NATS for real-time pub/sub.
//
// Subject naming:
//   guild.{guildID}.channel.message.{channelID}   → text message in guild channel
//   guild.{guildID}.channel.typing.{channelID}    → typing in guild channel
//   guild.{guildID}.channel.voice.{channelID}     → voice state in guild channel
//   guild.{guildID}.channel.voicechat.{channelID} → text chat in voice channel
//   guild.{guildID}.events                        → guild-level events
//   dm.{userID}.message.{channelID}               → DM message
//   dm.{userID}.typing.{channelID}                → DM typing
//   friend.{userID}.activity                      → friend presence/profile
//
// Wildcard subscriptions:
//   guild.{guildID}.channel.message.*  → all text channels in a guild
//   guild.{guildID}.channel.voice.*    → all voice state in a guild
//   dm.{userID}.message.*              → all DMs for a user
//   dm.{userID}.typing.*               → all DM typing for a user
type Hub struct {
	nc *nats.Conn
}

func NewHub(cfg config.NATSConfig) (*Hub, error) {
	nc, err := nats.Connect(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to nats: %w", err)
	}
	logger.Log.Info().Str("url", cfg.URL).Msg("connected to nats")
	return &Hub{nc: nc}, nil
}

func (h *Hub) Publish(subject string, data any) error {
	if h == nil || h.nc == nil {
		return nil
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return h.nc.Publish(subject, b)
}

func (h *Hub) Subscribe(subject string) (*Subscription, error) {
	if h == nil || h.nc == nil {
		return nil, fmt.Errorf("nats not connected")
	}
	ch := make(chan []byte, 128)
	sub, err := h.nc.Subscribe(subject, func(msg *nats.Msg) {
		select {
		case ch <- msg.Data:
		default:
		}
	})
	if err != nil {
		return nil, err
	}
	return &Subscription{sub: sub, Ch: ch}, nil
}

func (h *Hub) SubscribeMulti(subjects []string) (*MultiSubscription, error) {
	if h == nil || h.nc == nil {
		return nil, fmt.Errorf("nats not connected")
	}
	ch := make(chan SubjectMessage, 256)
	var subs []*nats.Subscription
	for _, subj := range subjects {
		s := subj
		sub, err := h.nc.Subscribe(s, func(msg *nats.Msg) {
			select {
			case ch <- SubjectMessage{Subject: msg.Subject, Data: msg.Data}:
			default:
			}
		})
		if err != nil {
			for _, existing := range subs {
				existing.Unsubscribe()
			}
			return nil, err
		}
		subs = append(subs, sub)
	}
	return &MultiSubscription{subs: subs, Ch: ch}, nil
}

func (h *Hub) Close() {
	if h != nil && h.nc != nil {
		h.nc.Close()
	}
}

// ── Subject builders (publish - specific channel) ──

// Guild channel messages
func GuildChannelMessage(guildID, channelID string) string {
	return "guild." + guildID + ".channel.message." + channelID
}

// Guild channel typing
func GuildChannelTyping(guildID, channelID string) string {
	return "guild." + guildID + ".channel.typing." + channelID
}

// Guild voice state
func GuildChannelVoice(guildID, channelID string) string {
	return "guild." + guildID + ".channel.voice." + channelID
}

// Guild voice text chat
func GuildChannelVoiceChat(guildID, channelID string) string {
	return "guild." + guildID + ".channel.voicechat." + channelID
}

// Guild events
func GuildEvents(guildID string) string {
	return "guild." + guildID + ".events"
}

// DM message
func DmMessage(userID, channelID string) string {
	return "dm." + userID + ".message." + channelID
}

// DM channel lifecycle (create/update/delete/member changes) for a given user.
func DmChannels(userID string) string {
	return "dm." + userID + ".channels"
}

// DM voice-call signalling (incoming, accepted, rejected, ended, participant
// join/leave) targeted at a specific user.
func DmCall(userID string) string {
	return "dm." + userID + ".call"
}

// DM typing
func DmTyping(userID, channelID string) string {
	return "dm." + userID + ".typing." + channelID
}

// Friend activity
func FriendActivity(userID string) string {
	return "friend." + userID + ".activity"
}

// ── Wildcard subjects (subscribe - all channels at once) ──

// All text messages in a guild
func GuildAllMessages(guildID string) string {
	return "guild." + guildID + ".channel.message.*"
}

// All typing in a guild
func GuildAllTyping(guildID string) string {
	return "guild." + guildID + ".channel.typing.*"
}

// All voice state in a guild
func GuildAllVoice(guildID string) string {
	return "guild." + guildID + ".channel.voice.*"
}

// All voice chat in a guild
func GuildAllVoiceChat(guildID string) string {
	return "guild." + guildID + ".channel.voicechat.*"
}

// All DM messages for a user
func DmAllMessages(userID string) string {
	return "dm." + userID + ".message.*"
}

// All DM typing for a user
func DmAllTyping(userID string) string {
	return "dm." + userID + ".typing.*"
}

// ── Types ──

type Subscription struct {
	sub  *nats.Subscription
	Ch   chan []byte
	once sync.Once
}

func (s *Subscription) Unsubscribe() {
	s.once.Do(func() {
		s.sub.Unsubscribe()
		close(s.Ch)
	})
}

type SubjectMessage struct {
	Subject string
	Data    []byte
}

type MultiSubscription struct {
	subs []*nats.Subscription
	Ch   chan SubjectMessage
	once sync.Once
}

func (m *MultiSubscription) Unsubscribe() {
	m.once.Do(func() {
		for _, s := range m.subs {
			s.Unsubscribe()
		}
		close(m.Ch)
	})
}
