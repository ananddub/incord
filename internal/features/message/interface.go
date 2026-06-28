package message

import "context"

// DMChannelResolver creates/finds DM channels. Implemented by channel service.
type DMChannelResolver interface {
	GetOrCreateDMChannel(ctx context.Context, userID string, recipientIDs []string) (channelID string, err error)
}

// BlockChecker checks if a user is blocked. Implemented by user service.
type BlockChecker interface {
	IsBlocked(ctx context.Context, userID, targetID string) bool
}

// DMChannelLister lists all DM channels a user is part of with metadata.
type DMChannelLister interface {
	GetUserDMChannelIDs(ctx context.Context, userID string) ([]string, error)
	GetDMChannelMemberIDs(ctx context.Context, channelID string) ([]string, error)
}

// MediaResolver resolves uploaded media file IDs into concrete attachment
type MediaResolver interface {
	ResolveAttachment(ctx context.Context, fileID, uploaderID string) (filename, url, contentType string, size int64, err error)
}
