package channel

import (
	"context"

	"github.com/google/uuid"
)

// GuildResolver wraps channel.Service to implement message.ChannelGuildResolver.
type GuildResolver struct {
	svc *Service
}

func NewGuildResolver(svc *Service) *GuildResolver {
	return &GuildResolver{svc: svc}
}

// GetChannelGuildID returns the guild ID for a channel, or empty string for DMs/unknown.
func (r *GuildResolver) GetChannelGuildID(ctx context.Context, channelID string) string {
	ch, err := r.svc.GetChannel(ctx, channelID)
	if err != nil {
		return ""
	}
	if !ch.GuildID.Valid {
		return ""
	}
	return uuid.UUID(ch.GuildID.Bytes).String()
}
