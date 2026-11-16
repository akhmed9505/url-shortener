package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
	"github.com/jackc/pgx/v5/pgconn"
)

type repositoryStub struct {
	err   error
	calls int
}

func (r *repositoryStub) Create(_ context.Context, code, raw string) (domain.Link, error) {
	r.calls++
	return domain.Link{ID: 1, Code: code, OriginalURL: raw, CreatedAt: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}, r.err
}

func testHandler(repository *repositoryStub) *Handler {
	service := shortener.New(repository, func() (string, error) { return "AbC123z9", nil })
	return NewHandler(service, "https://sho.rt", slog.New(slog.NewJSONHandler(io.Discard, nil)))
}

func TestShortenCreated(t *testing.T) {
	for _, tc := range []struct{ body, code string }{
		{`{"url":"https://example.org/path?q=a%20b"}`, "AbC123z9"},
		{`{"url":"https://example.org/path?q=a%20b","alias":"My_alias"}`, "My_alias"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			repository := &repositoryStub{}
			router := NewRouter(testHandler(repository))
			request := httptest.NewRequest(http.MethodPost, "http://evil.example/shorten", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json; charset=UTF-8")
			request.Header.Set("X-Request-ID", "untrusted-secret")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusCreated || repository.calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, repository.calls, response.Body)
			}
			var body ShortenResponse
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			wantURL := "https://sho.rt/s/" + tc.code
			if body.Code != tc.code || body.URL != "https://example.org/path?q=a%20b" || body.ShortURL != wantURL || body.CreatedAt.IsZero() {
				t.Fatalf("unexpected response: %+v", body)
			}
			if response.Header().Get("Location") != wantURL || response.Header().Get("Content-Type") != "application/json" {
				t.Fatal("unexpected response headers")
			}
			if id := response.Header().Get("X-Request-ID"); id == "" || id == "untrusted-secret" {
				t.Fatal("server must generate its own request ID")
			}
		})
	}
}

func TestShortenRejectsMalformedRequests(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body, code string
		status                        int
	}{
		{"missing type", "", `{}`, "unsupported_media_type", 415},
		{"wrong type", "text/plain", `{}`, "unsupported_media_type", 415},
		{"malformed type", "application/json;", `{}`, "invalid_url", 400},
		{"invalid parameter", "application/json; charset", `{}`, "unsupported_media_type", 415},
		{"wrong encoding", "application/json; charset=utf-16", `{}`, "unsupported_media_type", 415},
		{"unknown parameter", "application/json; profile=test", `{}`, "unsupported_media_type", 415},
		{"empty body", "application/json", ``, "invalid_json", 400},
		{"null", "application/json", `null`, "invalid_json", 400},
		{"array", "application/json", `[]`, "invalid_json", 400},
		{"truncated", "application/json", `{"url":`, "invalid_json", 400},
		{"trailing object", "application/json", `{} {}`, "invalid_json", 400},
		{"trailing garbage", "application/json", `{} junk`, "invalid_json", 400},
		{"unknown field", "application/json", `{"url":"https://example.org","extra":true}`, "invalid_json", 400},
		{"duplicate", "application/json", `{"url":"https://example.org","url":"https://other.org"}`, "invalid_json", 400},
		{"wrong case", "application/json", `{"URL":"https://example.org"}`, "invalid_json", 400},
		{"null url", "application/json", `{"url":null}`, "invalid_json", 400},
		{"null alias", "application/json", `{"url":"https://example.org","alias":null}`, "invalid_json", 400},
		{"numeric alias", "application/json", `{"url":"https://example.org","alias":123}`, "invalid_json", 400},
		{"empty alias", "application/json", `{"url":"https://example.org","alias":""}`, "invalid_alias", 400},
		{"missing url", "application/json", `{}`, "invalid_url", 400},
		{"credentials", "application/json", `{"url":"https://user:secret@example.org"}`, "invalid_url", 400},
		{"backslash", "application/json", `{"url":"https://example.org\\evil"}`, "invalid_url", 400},
		{"large body", "application/json", strings.Repeat(" ", 4097), "body_too_large", 413},
		{"invalid UTF-8", "application/json", "{\"url\":\"https://example.org/\xff\"}", "invalid_json", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &repositoryStub{}
			request := httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader(tc.body))
			if tc.contentType != "" {
				request.Header.Set("Content-Type", tc.contentType)
			}
			response := httptest.NewRecorder()
			NewRouter(testHandler(repository)).ServeHTTP(response, request)
			assertAPIError(t, response, tc.status, tc.code)
			if repository.calls != 0 {
				t.Fatal("repository called for invalid request")
			}
		})
	}
}

func TestShortenBodyLimitIncludesTrailingWhitespace(t *testing.T) {
	const payload = `{"url":"https://example.org"}`
	for _, size := range []int{4096, 4097} {
		request := httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader(payload+strings.Repeat(" ", size-len(payload))))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		NewRouter(testHandler(&repositoryStub{})).ServeHTTP(response, request)
		want := http.StatusCreated
		if size > 4096 {
			want = http.StatusRequestEntityTooLarge
		}
		if response.Code != want {
			t.Fatalf("size=%d status=%d", size, response.Code)
		}
	}
}

func TestShortenRejectsDuplicateContentType(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader(`{}`))
	request.Header.Add("Content-Type", "application/json")
	request.Header.Add("Content-Type", "text/plain")
	response := httptest.NewRecorder()
	NewRouter(testHandler(&repositoryStub{})).ServeHTTP(response, request)
	assertAPIError(t, response, 415, "unsupported_media_type")
}

func TestShortenMapsErrorsWithoutLeakingDetails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		body   string
		status int
		code   string
	}{
		{"alias conflict", domain.ErrCodeConflict, `{"url":"https://example.org","alias":"taken"}`, 409, "alias_taken"},
		{"exhaustion", domain.ErrCodeConflict, `{"url":"https://example.org"}`, 500, "code_generation_exhausted"},
		{"unavailable", fmt.Errorf("%w: password=secret", domain.ErrStorageUnavailable), `{"url":"https://example.org"}`, 503, "storage_unavailable"},
		{"unexpected", &pgconn.PgError{Code: "23505", Message: "SQL password=secret"}, `{"url":"https://example.org"}`, 500, "internal_error"},
		{"deadline", context.DeadlineExceeded, `{"url":"https://example.org"}`, 504, "request_timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := testHandler(&repositoryStub{err: tc.err})
			handler.log = slog.New(slog.NewJSONHandler(&logs, nil))
			request := httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader(tc.body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			NewRouter(handler).ServeHTTP(response, request)
			assertAPIError(t, response, tc.status, tc.code)
			for _, output := range []string{response.Body.String(), logs.String()} {
				if strings.Contains(output, "secret") || strings.Contains(output, "SQL") || strings.Contains(output, "example.org") {
					t.Fatalf("sensitive information exposed: %s", output)
				}
			}
		})
	}
}

func assertAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body)
	}
	var body ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code || body.Error.Message == "" || body.RequestID == "" || body.RequestID != response.Header().Get("X-Request-ID") {
		t.Fatalf("invalid error response: %+v", body)
	}
	if response.Header().Get("Content-Type") != "application/json" {
		t.Fatal("missing JSON content type")
	}
}
