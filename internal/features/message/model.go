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
	ErrReplyParentNotFound      = errors.New("reply target message not found or deleted")
	ErrEditWindowExpired        = errors.New("message is too old to edit")
	ErrForwardSourceNotFound    = errors.New("forwarded source message not found or deleted")
)

// editWindow is the maximum age at which a message author may still edit
// their own message. Mirrors common chat-app semantics (Discord allows
// unlimited; we cap at 24h to avoid unbounded edit history).
const EditWindow = 24 * time.Hour

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
	Deleted   bool
	UpdatedAt *time.Time
	// Forward source — zero values when this message is not a forward.
	ForwardedFromChannelID gocql.UUID
	ForwardedFromMessageID gocql.UUID
	ForwardedFromAuthorID  gocql.UUID
	// User IDs explicitly @-mentioned by the author.
	MentionUserIDs []gocql.UUID
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

// EditHistory tracks previous versions of a message.
type EditHistory struct {
	ChannelID  gocql.UUID
	MessageID  gocql.UUID
	OldContent string
	EditedAt   time.Time
}

// ReadState tracks the last-read position per user per channel.
type ReadState struct {
	UserID            gocql.UUID
	ChannelID         gocql.UUID
	LastReadMessageID gocql.UUID
	MentionCount      int
}

// Attachment is a single file attached to a message, mirrored in Scylla
// from the media service so clients can render it without a second lookup.
type Attachment struct {
	ID          gocql.UUID
	Filename    string
	URL         string // presigned GET URL; rebuilt on read
	ContentType string
	Size        int64
}
