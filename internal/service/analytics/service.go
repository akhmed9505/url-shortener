package analytics

import (
	"context"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

// Service validates analytics queries and reads stored visits.
type Service struct{ repository Repository }

// New creates an analytics service with the supplied repository.
func New(repository Repository) *Service { return &Service{repository: repository} }

// Summary returns consistent aggregate visit counts for the selected period.
func (s *Service) Summary(ctx context.Context, code, from, to string, now time.Time) (domain.Analytics, error) {
	if err := ctx.Err(); err != nil {
		return domain.Analytics{}, err
	}
	if !domain.ValidCode(code) {
		return domain.Analytics{}, &domain.ValidationError{Field: "code", Message: "Invalid short code"}
	}
	period, err := parsePeriod(from, to, now)
	if err != nil {
		return domain.Analytics{}, err
	}
	return s.repository.Summary(ctx, code, period)
}

func parsePeriod(from, to string, now time.Time) (domain.AnalyticsPeriod, error) {
	invalid := &domain.ValidationError{Field: "period", Message: "from and to must be paired YYYY-MM-DD dates defining a UTC interval of 1 to 366 days"}
	if from == "" && to == "" {
		today := now.UTC().Truncate(24 * time.Hour)
		return domain.AnalyticsPeriod{From: today.AddDate(0, 0, -29), To: today.AddDate(0, 0, 1)}, nil
	}
	start, err := time.Parse(time.DateOnly, from)
	if err != nil || start.Year() < 1 || start.Format(time.DateOnly) != from {
		return domain.AnalyticsPeriod{}, invalid
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil || end.Year() < 1 || end.Format(time.DateOnly) != to || !end.After(start) || end.After(start.AddDate(0, 0, 366)) {
		return domain.AnalyticsPeriod{}, invalid
	}
	return domain.AnalyticsPeriod{From: start, To: end}, nil
}

// VisitPage holds a period, ordered visits and an optional continuation cursor.
type VisitPage struct {
	Code       string
	Period     domain.AnalyticsPeriod
	Items      []domain.Visit
	NextCursor *string
}

// Visits validates the period and cursor and returns a page of visits.
func (s *Service) Visits(ctx context.Context, code, from, to string, limit int, cursor string, now time.Time) (VisitPage, error) {
	if err := ctx.Err(); err != nil {
		return VisitPage{}, err
	}
	if !domain.ValidCode(code) {
		return VisitPage{}, &domain.ValidationError{Field: "code", Message: "Invalid short code"}
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 {
		return VisitPage{}, &domain.ValidationError{Field: "limit", Message: "limit must be between 1 and 100"}
	}
	if cursor != "" && (from == "" || to == "") {
		return VisitPage{}, errInvalidCursor
	}
	period, err := parsePeriod(from, to, now)
	if err != nil {
		return VisitPage{}, err
	}
	var after *domain.VisitPosition
	if cursor != "" {
		position, err := decodeCursor(cursor, code, period)
		if err != nil {
			return VisitPage{}, err
		}
		after = &position
	}
	items, err := s.repository.ListVisits(ctx, code, period, after, limit+1)
	if err != nil {
		return VisitPage{}, err
	}
	page := VisitPage{Code: code, Period: period, Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		next, err := encodeCursor(code, period, page.Items[limit-1])
		if err != nil {
			return VisitPage{}, err
		}
		page.NextCursor = &next
	}
	return page, nil
}
