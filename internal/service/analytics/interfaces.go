package analytics

import (
	"context"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

// Repository reads aggregate summaries and ordered visit pages.
type Repository interface {
	// Summary returns visit aggregates for a validated period.
	Summary(context.Context, string, domain.AnalyticsPeriod) (domain.Analytics, error)
	// ListVisits reads a descending keyset page of visits.
	ListVisits(context.Context, string, domain.AnalyticsPeriod, *domain.VisitPosition, int) ([]domain.Visit, error)
}
