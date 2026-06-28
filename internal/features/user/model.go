package user

import "errors"

var (
	ErrUserNotFound        = errors.New("user not found")
	ErrCannotFriendSelf    = errors.New("cannot send friend request to yourself")
	ErrAlreadyFriends      = errors.New("already friends")
	ErrFriendRequestExists = errors.New("friend request already exists")
	ErrNoFriendRequest     = errors.New("no pending friend request found")
	ErrNotFriends          = errors.New("not friends")
	ErrUserBlocked         = errors.New("user is blocked")
	ErrAlreadyBlocked      = errors.New("user is already blocked")
	ErrNotBlocked          = errors.New("user is not blocked")
	ErrUsernameAlreadySet  = errors.New("username is already set")
)
