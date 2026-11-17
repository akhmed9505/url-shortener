package domain

import "time"

// Visit records a link visit with its UTC time and normalized User-Agent.
type Visit struct {
	ID        int64
	LinkID    int64
	VisitedAt time.Time
	UserAgent string
}

// VisitPosition is the exclusive boundary for descending keyset pagination.
type VisitPosition struct {
	VisitedAt time.Time
	ID        int64
}
