//go:build integration

package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	httpdelivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	"github.com/akhmed9505/url-shortener/internal/domain"
	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	"github.com/akhmed9505/url-shortener/internal/service/analytics"
	"github.com/jackc/pgx/v5/pgxpool"
)

func visitsServer(t *testing.T, pool *pgxpool.Pool) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	handler := httpdelivery.NewHandler(nil, analytics.New(pgrepo.NewAnalyticsRepository(pool)), pool.Ping, "https://sho.rt", log)
	server := httptest.NewServer(httpdelivery.NewRouter(handler))
	server.Client().Timeout = 5 * time.Second
	t.Cleanup(server.Close)
	return server
}

func fetchVisits(t *testing.T, server *httptest.Server, code string, query url.Values) httpdelivery.VisitsResponse {
	t.Helper()
	response, err := server.Client().Get(server.URL + "/analytics/" + code + "/visits?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var page httpdelivery.VisitsResponse
	if response.StatusCode != 200 {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if page.Code != code || page.Items == nil {
		t.Fatalf("page=%+v", page)
	}
	return page
}

func TestVisitPaginationAndIsolation(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	other, err := pgrepo.NewLinkRepository(pool).Create(t.Context(), "other", "https://example.org/other")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, at := range []string{
		"2024-02-28T23:59:59.999999Z", "2024-02-29T00:00:00Z",
		"2024-03-01T12:00:00.123456Z", "2024-03-01T12:00:00.123456Z",
		"2024-03-01T23:00:00Z", "2024-02-29T23:00:00Z", "2024-03-02T00:00:00Z",
	} {
		timestamp, err := time.Parse(time.RFC3339Nano, at)
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := pool.QueryRow(t.Context(), `INSERT INTO visits(link_id,visited_at,user_agent) VALUES($1,$2,$3) RETURNING id`, link.ID, timestamp, "TestBrowser/я").Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	addAnalyticsVisit(t, pool, other.ID, "2024-02-29T01:00:00Z", "OtherBrowser")
	server := visitsServer(t, pool)
	query := url.Values{"from": {"2024-02-29"}, "to": {"2024-03-02"}, "limit": {"2"}}
	var got []string
	var firstCursor string
	for pageNumber := 0; pageNumber < 3; pageNumber++ {
		page := fetchVisits(t, server, link.Code, query)
		if page.From != "2024-02-29" || page.To != "2024-03-02" {
			t.Fatalf("page=%+v", page)
		}
		for _, item := range page.Items {
			if item.UserAgent != "TestBrowser/я" || item.VisitedAt.Location() != time.UTC {
				t.Fatalf("item=%+v", item)
			}
			got = append(got, item.ID)
		}
		if pageNumber < 2 {
			if page.NextCursor == nil || len(page.Items) != 2 {
				t.Fatalf("page=%+v", page)
			}
			query.Set("cursor", *page.NextCursor)
		} else if page.NextCursor != nil || len(page.Items) != 1 {
			t.Fatalf("last page=%+v", page)
		}
		if pageNumber == 0 {
			firstCursor = *page.NextCursor
			// New rows above the boundary must not shift later pages or duplicate rows.
			addAnalyticsVisit(t, pool, link.ID, "2024-03-01T23:59:59Z", "new")
		}
	}
	want := []string{ids[4], ids[3], ids[2], ids[5], ids[1]}
	if len(got) != len(want) {
		t.Fatalf("ids=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids=%v want=%v", got, want)
		}
	}
	// A cursor belonging to another link or period must be rejected.
	for _, target := range []struct{ code, from, to string }{
		{other.Code, "2024-02-29", "2024-03-02"}, {link.Code, "2024-02-28", "2024-03-02"},
		{link.Code, "2024-02-29", "2024-03-01"}, {link.Code, "", ""},
	} {
		query := url.Values{"cursor": {firstCursor}}
		if target.from != "" {
			query.Set("from", target.from)
			query.Set("to", target.to)
		}
		assertVisitsError(t, server, target.code, query, 400, "invalid_cursor")
	}
	// Even a forged, structurally valid cursor remains restricted by link_id and period in SQL.
	data, err := base64.RawURLEncoding.DecodeString(firstCursor)
	if err != nil {
		t.Fatal(err)
	}
	var cursor map[string]any
	if err := json.Unmarshal(data, &cursor); err != nil {
		t.Fatal(err)
	}
	cursor["code"] = other.Code
	data, err = json.Marshal(cursor)
	if err != nil {
		t.Fatal(err)
	}
	query.Set("cursor", base64.RawURLEncoding.EncodeToString(data))
	page := fetchVisits(t, server, other.Code, query)
	if len(page.Items) != 1 || page.Items[0].UserAgent != "OtherBrowser" || page.NextCursor != nil {
		t.Fatalf("other page=%+v", page)
	}
	// A valid position at the oldest row returns an empty final page.
	cursor["code"], cursor["last_id"], cursor["last_visited_at"] = link.Code, ids[1], "2024-02-29T00:00:00.000000Z"
	data, err = json.Marshal(cursor)
	if err != nil {
		t.Fatal(err)
	}
	query.Set("cursor", base64.RawURLEncoding.EncodeToString(data))
	page = fetchVisits(t, server, link.Code, query)
	if len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("end page=%+v", page)
	}
}

func assertVisitsError(t *testing.T, server *httptest.Server, code string, query url.Values, status int, errorCode string) {
	t.Helper()
	response, err := server.Client().Get(server.URL + "/analytics/" + code + "/visits?" + query.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body httpdelivery.ErrorResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status || body.Error.Code != errorCode {
		t.Fatalf("status=%d body=%+v", response.StatusCode, body)
	}
}

func TestVisitsEmptyHistoryDefaultsAndLimits(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	server := visitsServer(t, pool)
	page := fetchVisits(t, server, link.Code, nil)
	if len(page.Items) != 0 || page.NextCursor != nil {
		t.Fatalf("page=%+v", page)
	}
	start, err := time.Parse(time.DateOnly, page.From)
	if err != nil {
		t.Fatal(err)
	}
	end, err := time.Parse(time.DateOnly, page.To)
	if err != nil || end.Sub(start) != 30*24*time.Hour || time.Now().UTC().Before(start) || !time.Now().UTC().Before(end) {
		t.Fatalf("default period=%+v err=%v", page, err)
	}
	for range 105 {
		if err := pgrepo.NewVisitRepository(pool).Record(t.Context(), domain.Visit{LinkID: link.ID, VisitedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	page = fetchVisits(t, server, link.Code, nil)
	if len(page.Items) != 20 || page.NextCursor == nil {
		t.Fatalf("default page=%+v", page)
	}
	page = fetchVisits(t, server, link.Code, url.Values{"limit": {"100"}})
	if len(page.Items) != 100 || page.NextCursor == nil {
		t.Fatalf("maximum page=%+v", page)
	}
	last := fetchVisits(t, server, link.Code, url.Values{"limit": {"100"}, "cursor": {*page.NextCursor}, "from": {page.From}, "to": {page.To}})
	if len(last.Items) != 5 || last.NextCursor != nil {
		t.Fatalf("last page=%+v", last)
	}
	assertVisitsError(t, server, "missing", nil, 404, "link_not_found")
	assertVisitsError(t, server, link.Code, url.Values{"cursor": {"bad"}, "from": {page.From}, "to": {page.To}}, 400, "invalid_cursor")
}

func TestVisitsDeadline(t *testing.T) {
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
	server := visitsServer(t, pool)
	server.Client().Timeout = 5 * time.Second
	start := time.Now()
	assertVisitsError(t, server, link.Code, nil, 504, "request_timeout")
	if elapsed := time.Since(start); elapsed < 2500*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("deadline elapsed=%v", elapsed)
	}
}
