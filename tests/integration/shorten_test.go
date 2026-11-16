//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	httpdelivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	"github.com/akhmed9505/url-shortener/internal/domain"
	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newShortenServer(t *testing.T, pool *pgxpool.Pool, generate func() (string, error), logs io.Writer) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(logs, nil))
	service := shortener.New(pgrepo.NewLinkRepository(pool), pgrepo.NewVisitRepository(pool), missingCache{}, generate, 150*time.Millisecond, log)
	handler := httpdelivery.NewHandler(service, nil, "https://sho.rt", log)
	server := httptest.NewServer(httpdelivery.NewRouter(handler))
	t.Cleanup(server.Close)
	return server
}

func postShorten(t *testing.T, server *httptest.Server, body string) (int, http.Header, []byte) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.URL+"/shorten", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Host = "untrusted.example"
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, response.Header, data
}

func TestShortenHTTPPersistsLinks(t *testing.T) {
	pool, _ := newDatabase(t, true)
	server := newShortenServer(t, pool, shortener.GenerateCode, io.Discard)
	for _, tc := range []struct{ body, alias string }{
		{`{"url":"https://example.org/docs?q=a%20b","alias":"docs"}`, "docs"},
		{`{"url":"https://example.org/docs?q=a%20b"}`, ""},
	} {
		status, header, data := postShorten(t, server, tc.body)
		if status != http.StatusCreated {
			t.Fatalf("status=%d body=%s", status, data)
		}
		var body httpdelivery.ShortenResponse
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if tc.alias != "" && body.Code != tc.alias {
			t.Fatalf("code=%q", body.Code)
		}
		if tc.alias == "" && len(body.Code) != 8 {
			t.Fatalf("code=%q", body.Code)
		}
		wantURL := "https://sho.rt/s/" + body.Code
		if body.ShortURL != wantURL || header.Get("Location") != wantURL {
			t.Fatalf("wrong short URL: %+v", body)
		}
		stored, err := pgrepo.NewLinkRepository(pool).FindByCode(t.Context(), body.Code)
		if err != nil || stored.OriginalURL != body.URL || !stored.CreatedAt.Equal(body.CreatedAt) {
			t.Fatalf("stored=%+v response=%+v err=%v", stored, body, err)
		}
	}
}

func TestShortenHTTPConcurrentAlias(t *testing.T) {
	pool, _ := newDatabase(t, true)
	server := newShortenServer(t, pool, shortener.GenerateCode, io.Discard)
	const workers = 16
	start := make(chan struct{})
	type result struct {
		status int
		err    error
	}
	results := make(chan result, workers)
	for range workers {
		go func() {
			<-start
			response, err := server.Client().Post(server.URL+"/shorten", "application/json", strings.NewReader(`{"url":"https://example.org","alias":"shared"}`))
			if err != nil {
				results <- result{err: err}
				return
			}
			_, err = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			results <- result{response.StatusCode, err}
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range workers {
		outcome := <-results
		if outcome.err != nil {
			t.Error(outcome.err)
			continue
		}
		switch outcome.status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			t.Errorf("unexpected status %d", outcome.status)
		}
	}
	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM links").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestShortenHTTPForcedCollisionsAndExhaustion(t *testing.T) {
	pool, _ := newDatabase(t, true)
	repository := pgrepo.NewLinkRepository(pool)
	if _, err := repository.Create(t.Context(), "Taken123", "https://example.org/original"); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	server := newShortenServer(t, pool, func() (string, error) {
		if attempts.Add(1) < 3 {
			return "Taken123", nil
		}
		return "Fresh123", nil
	}, io.Discard)
	status, _, data := postShorten(t, server, `{"url":"https://example.org/new"}`)
	if status != http.StatusCreated || attempts.Load() != 3 {
		t.Fatalf("status=%d attempts=%d body=%s", status, attempts.Load(), data)
	}
	stored, err := repository.FindByCode(t.Context(), "Fresh123")
	if err != nil || stored.OriginalURL != "https://example.org/new" {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	var exhausted atomic.Int32
	server = newShortenServer(t, pool, func() (string, error) {
		exhausted.Add(1)
		return "Taken123", nil
	}, io.Discard)
	status, _, data = postShorten(t, server, `{"url":"https://example.org/exhausted"}`)
	var body httpdelivery.ErrorResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if status != 500 || body.Error.Code != "code_generation_exhausted" || exhausted.Load() != 5 {
		t.Fatalf("status=%d attempts=%d body=%s", status, exhausted.Load(), data)
	}
	original, err := repository.FindByCode(t.Context(), "Taken123")
	if err != nil || original.OriginalURL != "https://example.org/original" {
		t.Fatalf("original=%+v err=%v", original, err)
	}
}

func TestShortenHTTPHidesDatabaseErrors(t *testing.T) {
	pool, _ := newDatabase(t, true)
	if _, err := pool.Exec(t.Context(), "DROP TABLE links CASCADE"); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	server := newShortenServer(t, pool, shortener.GenerateCode, &logs)
	status, _, data := postShorten(t, server, `{"url":"https://example.org/?token=private-secret"}`)
	server.Close()
	if status != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", status, data)
	}
	for _, private := range []string{"private-secret", "42P01", "does not exist", "INSERT", "RETURNING"} {
		if strings.Contains(string(data), private) || strings.Contains(logs.String(), private) {
			t.Fatalf("private data exposed: %q", private)
		}
	}
}

// Existing PostgreSQL tests deliberately exercise the uncached path.
// Redis integration tests use the real Redis adapter with unique keys.
type missingCache struct{}

func (missingCache) Get(context.Context, string) (domain.Link, bool, error) {
	return domain.Link{}, false, nil
}
func (missingCache) Set(context.Context, domain.Link) error { return nil }
