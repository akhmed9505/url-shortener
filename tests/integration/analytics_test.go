//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"

	httpdelivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	"github.com/akhmed9505/url-shortener/internal/domain"
	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	"github.com/akhmed9505/url-shortener/internal/service/analytics"
	"github.com/jackc/pgx/v5/pgxpool"
)

func addAnalyticsVisit(t *testing.T, pool *pgxpool.Pool, id int64, at, agent string) {
	t.Helper()
	timestamp, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := pgrepo.NewVisitRepository(pool).Record(t.Context(), domain.Visit{LinkID: id, VisitedAt: timestamp, UserAgent: agent}); err != nil {
		t.Fatal(err)
	}
}
func checkSummaryTotals(t *testing.T, summary domain.Analytics) {
	t.Helper()
	var days, months, agents int64
	for _, d := range summary.Daily {
		days += d.Count
	}
	for _, m := range summary.Monthly {
		months += m.Count
	}
	for _, a := range summary.UserAgents {
		agents += a.Count
	}
	if days != summary.PeriodVisits || months != days || agents+summary.OtherUserAgentsVisits != days {
		t.Fatalf("inconsistent summary=%+v", summary)
	}
}
func TestAnalyticsAggregationsAndBoundaries(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	for _, v := range []struct{ at, agent string }{
		{"2024-02-28T23:59:59.999999Z", "before"},
		{"2024-02-29T00:00:00Z", "A"}, {"2024-03-01T02:59:59+03:00", "B"},
		{"2024-03-01T00:00:00Z", "A"}, {"2024-03-02T00:00:00Z", "after"},
	} {
		addAnalyticsVisit(t, pool, link.ID, v.at, v.agent)
	}
	other, err := pgrepo.NewLinkRepository(pool).Create(t.Context(), "other", "https://example.org")
	if err != nil {
		t.Fatal(err)
	}
	addAnalyticsVisit(t, pool, other.ID, "2024-03-01T00:00:00Z", "unrelated")
	summary, err := pgrepo.NewAnalyticsRepository(pool).Summary(t.Context(), link.Code, analyticsPeriod("2024-02-29", "2024-03-02"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalVisits != 5 || summary.PeriodVisits != 3 || len(summary.Daily) != 2 || summary.Daily[0].Count != 2 || summary.Daily[1].Count != 1 || len(summary.Monthly) != 2 || summary.Monthly[0].Count != 2 || summary.UserAgents[0].UserAgent != "A" || summary.UserAgents[0].Count != 2 {
		t.Fatalf("summary=%+v", summary)
	}
	checkSummaryTotals(t, summary)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpdelivery.NewHandler(nil, analytics.New(pgrepo.NewAnalyticsRepository(pool)), "https://sho.rt", log)
	server := httptest.NewServer(httpdelivery.NewRouter(handler))
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/analytics/" + link.Code + "?from=2024-02-29&to=2024-03-02")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body httpdelivery.AnalyticsResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || body.TotalVisits != 5 || body.PeriodVisits != 3 || body.From != "2024-02-29" || body.To != "2024-03-02" || body.URL != link.OriginalURL || body.Daily[0].Date != "2024-02-29" {
		t.Fatalf("status=%d body=%+v", response.StatusCode, body)
	}
	missing, err := server.Client().Get(server.URL + "/analytics/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != 404 {
		t.Fatalf("status=%d", missing.StatusCode)
	}
}
func TestAnalyticsEmptyHistoryAndZeroBuckets(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	repo := pgrepo.NewAnalyticsRepository(pool)
	for _, p := range []domain.AnalyticsPeriod{analyticsPeriod("2024-01-01", "2025-01-01"), analyticsPeriod("2024-02-29", "2024-04-02")} {
		summary, err := repo.Summary(t.Context(), link.Code, p)
		if err != nil {
			t.Fatal(err)
		}
		if summary.TotalVisits != 0 || summary.PeriodVisits != 0 || len(summary.Daily) != int(p.To.Sub(p.From)/(24*time.Hour)) || len(summary.Monthly) < 3 || summary.UserAgents == nil {
			t.Fatalf("summary=%+v", summary)
		}
		if summary.Daily[0].Date != p.From.Format(time.DateOnly) || summary.Daily[len(summary.Daily)-1].Date != p.To.AddDate(0, 0, -1).Format(time.DateOnly) {
			t.Fatalf("days=%+v", summary.Daily)
		}

		if len(summary.Daily) == 366 && (len(summary.Monthly) != 12 || summary.Daily[59].Date != "2024-02-29" || summary.Monthly[11].Month != "2024-12") {
			t.Fatalf("unexpected leap-year calendar=%+v", summary)
		}
		checkSummaryTotals(t, summary)
	}
	if _, err := repo.Summary(t.Context(), "unknown", analyticsPeriod("2024-01-01", "2025-01-01")); !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("err=%v", err)
	}
	// A nonempty history outside the period must still leave zero buckets and agents.
	addAnalyticsVisit(t, pool, link.ID, "2020-01-01T00:00:00Z", "old")
	summary, err := repo.Summary(t.Context(), link.Code, analyticsPeriod("2024-01-01", "2024-02-01"))
	if err != nil || summary.TotalVisits != 1 || summary.PeriodVisits != 0 || len(summary.UserAgents) != 0 {
		t.Fatalf("summary=%+v err=%v", summary, err)
	}
}
func TestAnalyticsTopTwentyUserAgents(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	for i := 24; i >= 0; i-- {
		addAnalyticsVisit(t, pool, link.ID, "2024-02-29T12:00:00Z", fmt.Sprintf("Agent-%02d", i))
	}
	addAnalyticsVisit(t, pool, link.ID, "2024-02-29T12:00:00Z", "Agent-24")
	addAnalyticsVisit(t, pool, link.ID, "2024-02-29T12:00:00Z", "")
	summary, err := pgrepo.NewAnalyticsRepository(pool).Summary(t.Context(), link.Code, analyticsPeriod("2024-02-29", "2024-03-01"))
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.UserAgents) != 20 || summary.UserAgents[0].UserAgent != "Agent-24" || summary.UserAgents[0].Count != 2 || summary.UserAgents[1].UserAgent != "" || summary.OtherUserAgentsVisits != 6 {
		t.Fatalf("summary=%+v", summary)
	}
	for i := 2; i < 20; i++ {
		if summary.UserAgents[i].UserAgent != fmt.Sprintf("Agent-%02d", i-2) {
			t.Fatalf("agents=%+v", summary.UserAgents)
		}
	}
	checkSummaryTotals(t, summary)
}
func TestAnalyticsRepeatableReadSnapshot(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(t.Context(), "LOCK TABLE visits IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		summary domain.Analytics
		err     error
	}
	done := make(chan result, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		summary, err := pgrepo.NewAnalyticsRepository(pool).Summary(ctx, link.Code, analyticsPeriod("2024-02-29", "2024-03-01"))
		done <- result{summary, err}
	}()
	// The links SELECT establishes the snapshot. Wait until the following count is blocked.
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	waiting := false
	for !waiting {
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
 AND wait_event_type='Lock' AND query LIKE 'SELECT count(*), count(*) FILTER%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if !waiting {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-ticker.C:
			}
		}
	}
	if _, err := blocker.Exec(t.Context(), `INSERT INTO visits(link_id,visited_at,user_agent) VALUES($1,'2024-02-29T12:00:00Z','concurrent')`, link.ID); err != nil {
		t.Fatal(err)
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := <-done
	if got.err != nil {
		t.Fatal(got.err)
	}
	if got.summary.TotalVisits != 0 || got.summary.PeriodVisits != 0 {
		t.Fatalf("snapshot=%+v", got.summary)
	}
	checkSummaryTotals(t, got.summary)
	next, err := pgrepo.NewAnalyticsRepository(pool).Summary(t.Context(), link.Code, analyticsPeriod("2024-02-29", "2024-03-01"))
	if err != nil || next.TotalVisits != 1 || next.PeriodVisits != 1 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
	checkSummaryTotals(t, next)
}

func TestAnalyticsDeadline(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(t.Context(), "LOCK TABLE links IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpdelivery.NewHandler(nil, analytics.New(pgrepo.NewAnalyticsRepository(pool)), "https://sho.rt", log)
	server := httptest.NewServer(httpdelivery.NewRouter(handler))
	defer server.Close()
	start := time.Now()
	response, err := server.Client().Get(server.URL + "/analytics/" + link.Code)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body httpdelivery.ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 504 || body.Error.Code != "request_timeout" || time.Since(start) > 5*time.Second {
		t.Fatalf("status=%d body=%+v elapsed=%v", response.StatusCode, body, time.Since(start))
	}
}

func analyticsPeriod(from, to string) domain.AnalyticsPeriod {
	start, err := time.Parse(time.DateOnly, from)
	if err != nil {
		panic(err)
	}
	end, err := time.Parse(time.DateOnly, to)
	if err != nil {
		panic(err)
	}
	return domain.AnalyticsPeriod{From: start, To: end}
}
