package media

import "errors"

var (
	ErrFileTooLarge       = errors.New("file size exceeds maximum allowed size of 25MB")
	ErrInvalidContentType = errors.New("content type is not allowed")
)
