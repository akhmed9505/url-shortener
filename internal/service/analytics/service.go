package analytics

import (
	"context"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

type Repository interface {
	Summary(context.Context, string, domain.AnalyticsPeriod) (domain.Analytics, error)
	ListVisits(context.Context, string, domain.AnalyticsPeriod, *domain.VisitPosition, int) ([]domain.Visit, error)
}

type Service struct{ repository Repository }

func New(repository Repository) *Service { return &Service{repository: repository} }

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
