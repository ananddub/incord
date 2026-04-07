package channel

import "errors"

var (
	ErrChannelNotFound    = errors.New("channel not found")
	ErrGuildIDRequired    = errors.New("guild_id is required for guild channels")
	ErrNameRequired       = errors.New("channel name is required")
	ErrNotGuildMember     = errors.New("user is not a member of this guild")
	ErrInvalidChannelType = errors.New("invalid channel type")
	ErrRecipientRequired  = errors.New("at least one recipient is required for DM channel")
	ErrInvalidUUID             = errors.New("invalid UUID format")
	ErrInsufficientPermissions = errors.New("insufficient permissions")
)
