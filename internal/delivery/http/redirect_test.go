package http

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
)

type visitStub struct {
	visits []domain.Visit
	record func(context.Context, domain.Visit) error
}

func (s *visitStub) Record(ctx context.Context, visit domain.Visit) error {
	s.visits = append(s.visits, visit)
	if s.record != nil {
		return s.record(ctx, visit)
	}
	return nil
}

func redirectRouter(repository *repositoryStub, visits *visitStub, timeout time.Duration) http.Handler {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	service := shortener.New(repository, visits, missingCache{}, shortener.GenerateCode, timeout, log)
	return NewRouter(NewHandler(service, "https://sho.rt", log))
}

func TestRedirect(t *testing.T) {
	visits := &visitStub{}
	router := redirectRouter(&repositoryStub{}, visits, 150*time.Millisecond)
	request := httptest.NewRequest(http.MethodGet, "/s/docs", nil)
	request.Header.Set("User-Agent", strings.Repeat("я", 513))
	before := time.Now().UTC()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "https://example.org/target" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	if len(visits.visits) != 1 {
		t.Fatalf("visits=%d", len(visits.visits))
	}
	visit := visits.visits[0]
	if visit.VisitedAt.Before(before) || visit.VisitedAt.After(time.Now()) || visit.VisitedAt.Location() != time.UTC || len(visit.UserAgent) != 1024 || !utf8.ValidString(visit.UserAgent) {
		t.Fatalf("unexpected visit: %+v", visit)
	}
}

func TestRedirectLookupErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{domain.ErrLinkNotFound, 404, "link_not_found"},
		{domain.ErrStorageUnavailable, 503, "storage_unavailable"},
		{context.DeadlineExceeded, 504, "request_timeout"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			visits := &visitStub{}
			response := httptest.NewRecorder()
			redirectRouter(&repositoryStub{err: tc.err}, visits, 150*time.Millisecond).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/s/docs", nil))
			assertAPIError(t, response, tc.status, tc.code)
			if len(visits.visits) != 0 {
				t.Fatal("event recorded after failed lookup")
			}
		})
	}
}

func TestRedirectWriteTimeout(t *testing.T) {
	visits := &visitStub{record: func(ctx context.Context, _ domain.Visit) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	response := httptest.NewRecorder()
	redirectRouter(&repositoryStub{}, visits, 5*time.Millisecond).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/s/docs", nil))
	if response.Code != http.StatusFound {
		t.Fatalf("status=%d", response.Code)
	}
}

func TestRedirectHeadDoesNotAccessStorage(t *testing.T) {
	repository := &repositoryStub{}
	visits := &visitStub{}
	response := httptest.NewRecorder()
	redirectRouter(repository, visits, 150*time.Millisecond).ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/s/docs", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	if repository.calls != 0 || len(visits.visits) != 0 {
		t.Fatal("HEAD accessed storage")
	}
}

type missingCache struct{}

func (missingCache) Get(context.Context, string) (domain.Link, bool, error) {
	return domain.Link{}, false, nil
}
func (missingCache) Set(context.Context, domain.Link) error { return nil }

func TestRedirectUnicodeLocation(t *testing.T) {
	for _, original := range []string{
		"https://example.org/путь/😀?q=поиск&encoded=a%20b#якорь",
		"https://пример.рф/путь?q=поиск#якорь",
		"https://example.org/%D0%BF%D1%83%D1%82%D1%8C?q=%D1%82%D0%B5%D1%81%D1%82",
	} {
		t.Run(original, func(t *testing.T) {
			response := httptest.NewRecorder()
			redirectRouter(&repositoryStub{originalURL: original}, &visitStub{}, 150*time.Millisecond).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/s/docs", nil))
			location := response.Header().Get("Location")
			for _, char := range location {
				if char > 127 {
					t.Fatalf("non-ASCII Location %q", location)
				}
			}
			got, err := url.Parse(location)
			want, _ := url.Parse(original)
			if err != nil || response.Code != 302 || got.Host != want.Host || got.Path != want.Path || got.Query().Encode() != want.Query().Encode() || got.Fragment != want.Fragment {
				t.Fatalf("incorrect Unicode redirect: %q err=%v", location, err)
			}
		})
	}
}
