package message

import (
	"errors"
	"time"

	"github.com/gocql/gocql"
)

var (
	ErrMessageNotFound  = errors.New("message not found")
	ErrInvalidUUID      = errors.New("invalid UUID format")
	ErrChannelRequired  = errors.New("channel_id is required")
	ErrContentRequired  = errors.New("message content is required")
	ErrNotMessageAuthor = errors.New("you can only edit/delete your own messages")
	ErrEmojiRequired    = errors.New("emoji is required")
	ErrMessageRequired          = errors.New("message_id is required")
	ErrInsufficientPermissions  = errors.New("insufficient permissions")
	ErrUserBlocked              = errors.New("cannot send message to a blocked user or a user who blocked you")
)

// Message represents a message stored in ScyllaDB.
type Message struct {
	ChannelID gocql.UUID
	ID        gocql.UUID // TimeUUID
	AuthorID  gocql.UUID
	Content   string
	Type      int
	ReplyToID gocql.UUID
	Pinned    bool
	EditedAt  *time.Time
	CreatedAt time.Time
}

// Reaction represents a single user reaction on a message.
type Reaction struct {
	ChannelID gocql.UUID
	MessageID gocql.UUID
	Emoji     string
	UserID    gocql.UUID
}

// ReactionCount holds the aggregated reaction data for display.
type ReactionCount struct {
	Emoji string
	Count int32
	Me    bool
}

// ReadState tracks the last-read position per user per channel.
type ReadState struct {
	UserID            gocql.UUID
	ChannelID         gocql.UUID
	LastReadMessageID gocql.UUID
	MentionCount      int
}
