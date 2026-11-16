package domain

import "errors"

var (
	ErrLinkNotFound       = errors.New("link not found")
	ErrCodeConflict       = errors.New("short code already exists")
	ErrStorageUnavailable = errors.New("storage temporarily unavailable")
)
