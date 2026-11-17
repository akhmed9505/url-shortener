package handler

import (
	"context"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/akhmed9505/url-shortener/internal/service/analytics"
)

// Shortener provides link creation and resolution to HTTP handlers.
type Shortener interface {
	// Create stores a link with the requested code or alias.
	Create(context.Context, string, *string) (domain.Link, error)
	// Resolve resolves a link and records its visit.
	Resolve(context.Context, string, string, time.Time) (domain.Link, error)
}

// Analytics provides summaries and visit pages to HTTP handlers.
type Analytics interface {
	// Summary returns visit aggregates for a validated period.
	Summary(context.Context, string, string, string, time.Time) (domain.Analytics, error)
	// Visits returns a validated cursor page of visits.
	Visits(context.Context, string, string, string, int, string, time.Time) (analytics.VisitPage, error)
}
