package event

import (
	"context"
	"encoding/json"
)

// Envelope is the standard wrapper for all events published to Redpanda.
// Every service MUST publish events in this format for the gateway dispatcher to route them.
type Envelope struct {
	Type      string          `json:"type"`
	GuildID   string          `json:"guild_id,omitempty"`
	ChannelID string          `json:"channel_id,omitempty"`
	UserID    string          `json:"user_id,omitempty"`
	Data      json.RawMessage `json:"data"`
}

// PublishEvent wraps data in an Envelope and publishes to the given topic.
// This is the standard way all services should publish events.
func PublishEvent(ctx context.Context, p *Producer, topic string, key string, guildID string, channelID string, userID string, data any) error {
	if p == nil {
		return nil
	}

	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}

	env := Envelope{
		Type:      topic,
		GuildID:   guildID,
		ChannelID: channelID,
		UserID:    userID,
		Data:      raw,
	}

	return p.Publish(ctx, topic, key, env)
}
