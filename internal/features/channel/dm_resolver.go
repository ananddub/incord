package channel

import (
	"context"

	"github.com/google/uuid"
)

// DMResolver wraps channel.Service to implement message.DMChannelResolver.
type DMResolver struct {
	svc *Service
}

func NewDMResolver(svc *Service) *DMResolver {
	return &DMResolver{svc: svc}
}

// GetOrCreateDMChannel finds or creates a DM channel and returns its ID.
func (r *DMResolver) GetOrCreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (string, error) {
	ch, err := r.svc.CreateDMChannel(ctx, userID, recipientIDs)
	if err != nil {
		return "", err
	}
	return uuid.UUID(ch.ID.Bytes).String(), nil
}
