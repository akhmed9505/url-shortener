package domain

import "time"

// Link holds a stored short code, destination URL and creation time.
type Link struct {
	ID          int64
	Code        string
	OriginalURL string
	CreatedAt   time.Time
}
