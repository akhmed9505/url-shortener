package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

type repositoryStub struct {
	period domain.AnalyticsPeriod
	calls  int
	err    error
}

func (r *repositoryStub) Summary(_ context.Context, code string, period domain.AnalyticsPeriod) (domain.Analytics, error) {
	r.calls++
	r.period = period
	return domain.Analytics{Code: code, Period: period}, r.err
}

func TestPeriodValidation(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		valid    bool
	}{
		{"2024-02-29", "2024-03-01", true}, {"2024-01-01", "2025-01-01", true},
		{"2023-02-29", "2023-03-01", false}, {"2024-01-01", "2025-01-02", false},
		{"2024-03-01", "2024-03-01", false}, {"2024-03-02", "2024-03-01", false},
		{"2024-01-01", "", false}, {"", "2024-01-01", false}, {"2024-1-01", "2024-02-01", false},
		{"0000-01-01", "0000-02-01", false}, {"2024-01-01T00:00:00Z", "2024-02-01", false},
	} {
		t.Run(tc.from+"_"+tc.to, func(t *testing.T) {
			repo := &repositoryStub{}
			_, err := New(repo).Summary(t.Context(), "abc", tc.from, tc.to, time.Now())
			if (err == nil) != tc.valid {
				t.Fatalf("err=%v", err)
			}
			if !tc.valid {
				var validation *domain.ValidationError
				if !errors.As(err, &validation) || repo.calls != 0 {
					t.Fatalf("err=%v calls=%d", err, repo.calls)
				}
			}
		})
	}
}

func TestDefaultPeriodUsesUTC(t *testing.T) {
	repo := &repositoryStub{}
	now := time.Date(2024, 3, 1, 1, 0, 0, 0, time.FixedZone("UTC+3", 3*3600))
	_, err := New(repo).Summary(t.Context(), "abc", "", "", now)
	if err != nil || repo.period.From.Format(time.DateOnly) != "2024-01-31" || repo.period.To.Format(time.DateOnly) != "2024-03-01" || repo.period.From.Location() != time.UTC {
		t.Fatalf("period=%+v err=%v", repo.period, err)
	}
}

func TestSummaryErrors(t *testing.T) {
	repo := &repositoryStub{err: domain.ErrLinkNotFound}
	service := New(repo)
	if _, err := service.Summary(t.Context(), "abc", "", "", time.Now()); !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatal(err)
	}
	repo.calls = 0
	if _, err := service.Summary(t.Context(), "a/b", "", "", time.Now()); err == nil || repo.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, repo.calls)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Summary(ctx, "abc", "", "", time.Now()); !errors.Is(err, context.Canceled) || repo.calls != 0 {
		t.Fatalf("err=%v", err)
	}
}
