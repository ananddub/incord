package message

import (
	"errors"
	"time"

	"github.com/ananddub/ndiscord_backend/gen/scylladb"
	"github.com/gocql/gocql"
)

var (
	ErrMessageNotFound         = errors.New("message not found")
	ErrInvalidUUID             = errors.New("invalid UUID format")
	ErrChannelRequired         = errors.New("channel_id is required")
	ErrContentRequired         = errors.New("message content is required")
	ErrNotMessageAuthor        = errors.New("you can only edit/delete your own messages")
	ErrEmojiRequired           = errors.New("emoji is required")
	ErrMessageRequired         = errors.New("message_id is required")
	ErrInsufficientPermissions = errors.New("insufficient permissions")
	ErrUserBlocked             = errors.New("cannot send message to a blocked user or a user who blocked you")
	ErrReplyParentNotFound     = errors.New("reply target message not found or deleted")
	ErrEditWindowExpired       = errors.New("message is too old to edit")
	ErrForwardSourceNotFound   = errors.New("forwarded source message not found or deleted")
)

// editWindow is the maximum age at which a message author may still edit
// their own message. Mirrors common chat-app semantics (Discord allows
// unlimited; we cap at 24h to avoid unbounded edit history).
const EditWindow = 24 * time.Hour

type ReactionCount struct {
	user_id []string
	Emoji   string
	Count   int32
	Me      bool
}

type Message = scylladb.Messages
type Attachment = scylladb.MessageAttachments
type EditHistory = scylladb.MessageEditHistory

func uuidValue(v *gocql.UUID) gocql.UUID {
	if v == nil {
		return gocql.UUID{}
	}
	return *v
}

func stringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func boolValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}

func int64Value(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func timeValue(v *time.Time) time.Time {
	if v == nil {
		return time.Time{}
	}
	return *v
}

func uuidSliceValue(v *[]gocql.UUID) []gocql.UUID {
	if v == nil {
		return nil
	}
	return *v
}
