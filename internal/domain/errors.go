package domain

import "errors"

var (
	// ErrLinkNotFound reports that no link exists for the code.
	ErrLinkNotFound = errors.New("link not found")
	// ErrCodeConflict reports that the short code is already stored.
	ErrCodeConflict = errors.New("short code already exists")
	// ErrStorageUnavailable reports temporary storage unavailability.
	ErrStorageUnavailable = errors.New("storage temporarily unavailable")
)
