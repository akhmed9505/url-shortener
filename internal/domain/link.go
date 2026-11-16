package domain

import "time"

type Link struct {
	ID          int64
	Code        string
	OriginalURL string
	CreatedAt   time.Time
}
