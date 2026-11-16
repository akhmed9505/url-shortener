package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

type visitsRepositoryStub struct {
	repositoryStub
	items []domain.Visit
	after *domain.VisitPosition
	limit int
}

func (r *visitsRepositoryStub) ListVisits(_ context.Context, _ string, period domain.AnalyticsPeriod, after *domain.VisitPosition, limit int) ([]domain.Visit, error) {
	r.calls++
	r.period, r.after, r.limit = period, after, limit
	return r.items, r.err
}

func TestVisitsPages(t *testing.T) {
	period := cursorTestPeriod()
	repo := &visitsRepositoryStub{items: []domain.Visit{
		{ID: 3, VisitedAt: period.From}, {ID: 2, VisitedAt: period.From}, {ID: 1, VisitedAt: period.From},
	}}
	service := New(repo)
	page, err := service.Visits(t.Context(), "abc", "2024-02-29", "2024-03-02", 2, "", time.Now())
	if err != nil || len(page.Items) != 2 || page.NextCursor == nil || repo.limit != 3 || repo.after != nil {
		t.Fatalf("page=%+v err=%v limit=%d", page, err, repo.limit)
	}
	repo.items = repo.items[2:]
	page, err = service.Visits(t.Context(), "abc", "2024-02-29", "2024-03-02", 2, *page.NextCursor, time.Now())
	if err != nil || len(page.Items) != 1 || page.NextCursor != nil || repo.after.ID != 2 || !repo.after.VisitedAt.Equal(period.From) {
		t.Fatalf("page=%+v after=%+v err=%v", page, repo.after, err)
	}
	for _, limit := range []int{0, 1, 20, 100} {
		repo.items = nil
		page, err := service.Visits(t.Context(), "abc", "", "", limit, "", time.Now())
		want := limit + 1
		if limit == 0 {
			want = 21
		}
		if err != nil || page.NextCursor != nil || repo.limit != want {
			t.Fatalf("page=%+v err=%v limit=%d want=%d", page, err, repo.limit, want)
		}
	}
	// Exactly limit rows is the last page as well.
	repo.items = []domain.Visit{{ID: 1, VisitedAt: period.From}}
	page, err = service.Visits(t.Context(), "abc", "2024-02-29", "2024-03-02", 1, "", time.Now())
	if err != nil || len(page.Items) != 1 || page.NextCursor != nil {
		t.Fatalf("page=%+v err=%v", page, err)
	}
}

func TestVisitsValidationAndErrors(t *testing.T) {
	cursor, err := encodeCursor("abc", cursorTestPeriod(), domain.Visit{ID: 1, VisitedAt: cursorTestPeriod().From})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		code, from, to, cursor, field string
		limit                         int
	}{
		{"bad/code", "", "", "", "code", 20},
		{"abc", "", "", "", "limit", -1}, {"abc", "", "", "", "limit", 101},
		{"abc", "2024-01-01", "", "", "period", 20},
		{"abc", "2024-01-01", "2025-01-02", "", "period", 20},
		{"abc", "", "", cursor, "cursor", 20},
		{"abc", "2024-02-29", "", cursor, "cursor", 20},
		{"abc", "2024-02-29", "2024-03-02", "bad", "cursor", 20},
		{"other", "2024-02-29", "2024-03-02", cursor, "cursor", 20},
		{"abc", "2024-02-28", "2024-03-02", cursor, "cursor", 20},
	} {
		repo := &visitsRepositoryStub{}
		_, err := New(repo).Visits(t.Context(), tc.code, tc.from, tc.to, tc.limit, tc.cursor, time.Now())
		var validation *domain.ValidationError
		if !errors.As(err, &validation) || validation.Field != tc.field || repo.calls != 0 {
			t.Fatalf("case=%+v err=%v calls=%d", tc, err, repo.calls)
		}
	}
	for _, want := range []error{domain.ErrLinkNotFound, domain.ErrStorageUnavailable, context.DeadlineExceeded, errors.New("storage failure")} {
		repo := &visitsRepositoryStub{repositoryStub: repositoryStub{err: want}}
		_, err := New(repo).Visits(t.Context(), "abc", "", "", 20, "", time.Now())
		if !errors.Is(err, want) {
			t.Fatalf("err=%v want=%v", err, want)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	repo := &visitsRepositoryStub{}
	if _, err := New(repo).Visits(ctx, "abc", "", "", 20, "", time.Now()); !errors.Is(err, context.Canceled) || repo.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, repo.calls)
	}
}
