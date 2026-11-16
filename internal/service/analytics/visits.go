package analytics

import (
	"context"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

type VisitPage struct {
	Code       string
	Period     domain.AnalyticsPeriod
	Items      []domain.Visit
	NextCursor *string
}

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
