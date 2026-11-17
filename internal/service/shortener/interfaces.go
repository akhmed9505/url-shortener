package shortener

import (
	"context"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

// LinkRepository stores links and resolves short codes.
type LinkRepository interface {
	// Create stores a link with the requested code or alias.
	Create(ctx context.Context, code, originalURL string) (domain.Link, error)
	// FindByCode returns a link by its short code.
	FindByCode(ctx context.Context, code string) (domain.Link, error)
}

// VisitRepository records link visits.
type VisitRepository interface {
	// Record stores a visit event.
	Record(ctx context.Context, visit domain.Visit) error
}

// LinkCache caches stored links by short code.
type LinkCache interface {
	// Get returns a cached link and whether it exists.
	Get(ctx context.Context, code string) (domain.Link, bool, error)
	// Set caches a stored link.
	Set(ctx context.Context, link domain.Link) error
}
