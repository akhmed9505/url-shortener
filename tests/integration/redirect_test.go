//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	httpresponse "github.com/akhmed9505/url-shortener/internal/delivery/http/response"
	"github.com/akhmed9505/url-shortener/internal/domain"
	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newRedirectServer(t *testing.T, pool *pgxpool.Pool, logs io.Writer) *httptest.Server {
	t.Helper()
	server := newShortenServer(t, pool, shortener.GenerateCode, logs)
	server.Client().Timeout = 5 * time.Second
	server.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return server
}

func redirectRequest(t *testing.T, server *httptest.Server, method, code, userAgent string) (int, http.Header, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, server.URL+"/s/"+code, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("User-Agent", userAgent)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, body
}

func createRedirectLink(t *testing.T, pool *pgxpool.Pool) domain.Link {
	t.Helper()
	link, err := pgrepo.NewLinkRepository(pool).Create(t.Context(), "redirect", "https://example.org/target?token=private-secret")
	if err != nil {
		t.Fatal(err)
	}
	return link
}

func visitCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM visits").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRedirectSavesVisit(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	server := newRedirectServer(t, pool, io.Discard)
	before := time.Now().UTC().Truncate(time.Microsecond)
	status, headers, _ := redirectRequest(t, server, http.MethodGet, link.Code, "TestBrowser/1.0")
	if status != 302 || headers.Get("Location") != link.OriginalURL || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v", status, headers)
	}
	var visit domain.Visit
	err := pool.QueryRow(t.Context(), "SELECT id, link_id, visited_at, user_agent FROM visits").Scan(&visit.ID, &visit.LinkID, &visit.VisitedAt, &visit.UserAgent)
	if err != nil || visit.ID <= 0 || visit.LinkID != link.ID || visit.UserAgent != "TestBrowser/1.0" || visit.VisitedAt.Before(before) || visit.VisitedAt.After(time.Now().UTC()) {
		t.Fatalf("visit=%+v err=%v", visit, err)
	}
}

func TestRedirectMissingLinkAndHeadDoNotRecordEvents(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	server := newRedirectServer(t, pool, io.Discard)
	status, _, body := redirectRequest(t, server, http.MethodGet, "missing", "")
	var response httpresponse.ErrorResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if status != 404 || response.Error.Code != "link_not_found" {
		t.Fatalf("status=%d body=%s", status, body)
	}
	status, headers, body := redirectRequest(t, server, http.MethodHead, link.Code, "")
	if status != 405 || headers.Get("Allow") != "GET" || len(body) != 0 {
		t.Fatalf("status=%d headers=%v body=%s", status, headers, body)
	}
	if count := visitCount(t, pool); count != 0 {
		t.Fatalf("events=%d", count)
	}
}

func TestRedirectWriteFailureKeepsRedirect(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	if _, err := pool.Exec(t.Context(), "DROP TABLE visits"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := newRedirectServer(t, pool, &logs)
	status, headers, body := redirectRequest(t, server, http.MethodGet, link.Code, "private-secret")
	server.Close()
	if status != 302 || headers.Get("Location") != link.OriginalURL {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if !strings.Contains(logs.String(), "visit_write_failed") {
		t.Fatal("missing failure log")
	}
	for _, private := range []string{"private-secret", "42P01", "INSERT", "does not exist"} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("private log data: %q", private)
		}
	}
}

func TestRedirectWriteTimeoutKeepsRedirect(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	_, err := pool.Exec(t.Context(), `CREATE FUNCTION delay_visit() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN PERFORM pg_sleep(1); RETURN NEW; END $$;
	CREATE TRIGGER delay_visit BEFORE INSERT ON visits FOR EACH ROW EXECUTE FUNCTION delay_visit()`)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := newRedirectServer(t, pool, &logs)
	status, headers, _ := redirectRequest(t, server, http.MethodGet, link.Code, "TestBrowser")
	server.Close()
	if status != 302 || headers.Get("Location") != link.OriginalURL {
		t.Fatalf("status=%d headers=%v", status, headers)
	}
	if count := visitCount(t, pool); count != 0 {
		t.Fatalf("events=%d", count)
	}
	if !strings.Contains(logs.String(), `"reason":"timeout"`) {
		t.Fatalf("unexpected log: %s", &logs)
	}
}

func TestRedirectNormalizesUserAgent(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	server := newRedirectServer(t, pool, io.Discard)
	for _, tc := range []struct{ raw, want string }{
		{"", ""},
		{"bad\xffagent", "bad\uFFFDagent"},
		{strings.Repeat("я", 600), strings.Repeat("я", 512)},
		{strings.Repeat("a", 1023) + "я", strings.Repeat("a", 1023)},
	} {
		status, _, _ := redirectRequest(t, server, http.MethodGet, link.Code, tc.raw)
		if status != 302 {
			t.Fatalf("status=%d", status)
		}
		var stored string
		if err := pool.QueryRow(t.Context(), "SELECT user_agent FROM visits ORDER BY id DESC LIMIT 1").Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if stored != tc.want || len(stored) > 1024 || !utf8.ValidString(stored) {
			t.Fatalf("invalid stored User-Agent %q", stored)
		}
	}
}

func TestConcurrentRedirects(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	server := newRedirectServer(t, pool, io.Discard)
	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	for range workers {
		go func() {
			<-start
			response, err := server.Client().Get(server.URL + "/s/" + link.Code)
			if err != nil {
				results <- err
				return
			}
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != 302 {
				err = errors.New("unexpected redirect status")
			}
			results <- err
		}()
	}
	close(start)
	for range workers {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	if count := visitCount(t, pool); count != workers {
		t.Fatalf("events=%d want=%d", count, workers)
	}
}

func TestRedirectLookupDeadline(t *testing.T) {
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
	server := newRedirectServer(t, pool, io.Discard)
	status, _, body := redirectRequest(t, server, http.MethodGet, link.Code, "")
	if status != 504 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if count := visitCount(t, pool); count != 0 {
		t.Fatalf("events=%d", count)
	}
}

func TestRedirectPostgresUnavailable(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	server := newRedirectServer(t, pool, io.Discard)
	admin, err := pgx.Connect(t.Context(), os.Getenv("TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	database := pool.Config().ConnConfig.Database
	identifier := pgx.Identifier{database}.Sanitize()
	if _, err := admin.Exec(t.Context(), "ALTER DATABASE "+identifier+" ALLOW_CONNECTIONS false"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "ALTER DATABASE "+identifier+" ALLOW_CONNECTIONS true")
	if _, err := admin.Exec(t.Context(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", database); err != nil {
		t.Fatal(err)
	}
	pool.Reset()
	status, _, body := redirectRequest(t, server, http.MethodGet, link.Code, "")
	if status != 503 {
		t.Fatalf("status=%d body=%s", status, body)
	}
	if _, err := admin.Exec(t.Context(), "ALTER DATABASE "+identifier+" ALLOW_CONNECTIONS true"); err != nil {
		t.Fatal(err)
	}
	if count := visitCount(t, pool); count != 0 {
		t.Fatalf("events=%d", count)
	}
}

func TestVisitConstraints(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	repository := pgrepo.NewVisitRepository(pool)
	visit := domain.Visit{LinkID: link.ID, VisitedAt: time.Now().UTC(), UserAgent: strings.Repeat("a", 1024)}
	if err := repository.Record(t.Context(), visit); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name           string
		linkID, at, ua any
		state          string
	}{
		{"null link", nil, time.Now(), "", "23502"},
		{"missing link", link.ID + 1, time.Now(), "", "23503"},
		{"null time", link.ID, nil, "", "23502"},
		{"null agent", link.ID, time.Now(), nil, "23502"},
		{"long agent", link.ID, time.Now(), strings.Repeat("a", 1025), "23514"},
		{"byte limit", link.ID, time.Now(), strings.Repeat("я", 513), "23514"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), "INSERT INTO visits(link_id, visited_at, user_agent) VALUES ($1, $2, $3)", tc.linkID, tc.at, tc.ua)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.state {
				t.Fatalf("expected %s, got %v", tc.state, err)
			}
		})
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO visits(link_id, visited_at) VALUES ($1, $2)", link.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	var ua string
	if err := pool.QueryRow(t.Context(), "SELECT user_agent FROM visits ORDER BY id DESC LIMIT 1").Scan(&ua); err != nil || ua != "" {
		t.Fatalf("default UA=%q err=%v", ua, err)
	}
	var index string
	if err := pool.QueryRow(t.Context(), "SELECT indexdef FROM pg_indexes WHERE tablename='visits' AND indexname='visits_link_time_id_idx'").Scan(&index); err != nil || !strings.Contains(index, "(link_id, visited_at DESC, id DESC)") {
		t.Fatalf("index=%q err=%v", index, err)
	}
}
