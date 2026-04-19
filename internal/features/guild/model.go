package guild

import "errors"

var (
	ErrGuildNotFound    = errors.New("guild not found")
	ErrNotGuildOwner    = errors.New("only the guild owner can perform this action")
	ErrNotGuildMember   = errors.New("not a member of this guild")
	ErrAlreadyMember    = errors.New("already a member of this guild")
	ErrOwnerCannotLeave = errors.New("guild owner cannot leave the guild")
	ErrUserBanned       = errors.New("user is banned from this guild")
	ErrUserNotBanned    = errors.New("user is not banned from this guild")
	ErrInviteNotFound   = errors.New("invite not found")
	ErrInviteExpired    = errors.New("invite has expired")
	ErrInviteMaxUses    = errors.New("invite has reached maximum uses")
	ErrRoleNotFound     = errors.New("role not found")
	ErrMemberNotFound   = errors.New("member not found")
	ErrCannotKickOwner  = errors.New("cannot kick the guild owner")
	ErrCannotBanOwner          = errors.New("cannot ban the guild owner")
	ErrInsufficientPermissions = errors.New("insufficient permissions")
	ErrInvalidPermission       = errors.New("unknown permission name")
)
