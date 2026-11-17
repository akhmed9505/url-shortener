package handler_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/akhmed9505/url-shortener/internal/delivery/http/handler"
	. "github.com/akhmed9505/url-shortener/internal/delivery/http/response"
	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/akhmed9505/url-shortener/internal/service/analytics"
)

type visitsHTTPStub struct {
	analyticsStub
	items []domain.Visit
	limit int
}

func (s *visitsHTTPStub) ListVisits(ctx context.Context, _ string, _ domain.AnalyticsPeriod, _ *domain.VisitPosition, limit int) ([]domain.Visit, error) {
	s.calls++
	s.limit = limit
	deadline, _ := ctx.Deadline()
	s.timeout = time.Until(deadline)
	return s.items, s.err
}

func TestVisitsHTTP(t *testing.T) {
	for _, tc := range []struct {
		query, errorCode string
		err              error
		status           int
	}{
		{"", "", nil, 200}, {"?from=2024-02-29&to=2024-03-01&limit=100", "", nil, 200},
		{"?limit=0", "invalid_limit", nil, 400}, {"?limit=101", "invalid_limit", nil, 400},
		{"?limit=-1", "invalid_limit", nil, 400}, {"?limit=", "invalid_limit", nil, 400},
		{"?limit=1.5", "invalid_limit", nil, 400}, {"?limit=abc", "invalid_limit", nil, 400},
		{"?limit=9999999999999999999999", "invalid_limit", nil, 400},
		{"?limit=1&limit=2", "invalid_limit", nil, 400},
		{"?cursor=", "invalid_cursor", nil, 400}, {"?cursor=a&cursor=b", "invalid_cursor", nil, 400},
		{"?cursor=bad", "invalid_cursor", nil, 400},
		{"?cursor=bad&from=2024-02-29&to=2024-03-01", "invalid_cursor", nil, 400},
		{"?from=2024-02-29", "invalid_period", nil, 400},
		{"?from=2024-02-29&from=2024-03-01&to=2024-03-02", "invalid_period", nil, 400},
		{"?from=2024-01-01&to=2025-01-02", "invalid_period", nil, 400},
		{"", "link_not_found", domain.ErrLinkNotFound, 404},
		{"", "storage_unavailable", domain.ErrStorageUnavailable, 503},
		{"", "request_timeout", context.DeadlineExceeded, 504},
	} {
		t.Run(tc.query+tc.errorCode, func(t *testing.T) {
			stub := &visitsHTTPStub{analyticsStub: analyticsStub{err: tc.err}}
			handler := testHandler(&repositoryStub{})
			handler = NewHandler(nil, analytics.New(stub), nil, "https://sho.rt", testLogger())
			response := httptest.NewRecorder()
			NewRouter(handler).ServeHTTP(response, httptest.NewRequest("GET", "/analytics/abc/visits"+tc.query, nil))
			if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			if tc.status == 200 {
				var body VisitsResponse
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Items == nil || body.NextCursor != nil || body.Code != "abc" || stub.timeout <= 0 || stub.timeout > 3*time.Second {
					t.Fatalf("body=%+v timeout=%v", body, stub.timeout)
				}
				wantLimit := 21
				if tc.query != "" {
					wantLimit = 101
				}
				if stub.limit != wantLimit {
					t.Fatalf("repository limit=%d want=%d", stub.limit, wantLimit)
				}
			} else {
				var body ErrorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error.Code != tc.errorCode || body.RequestID == "" {
					t.Fatalf("body=%+v", body)
				}
				if tc.status == 400 && stub.calls != 0 {
					t.Fatalf("repository called for invalid request: %d", stub.calls)
				}
			}
		})
	}
}

func TestVisitsHTTPItemAndMethod(t *testing.T) {
	at := time.Date(2024, 2, 29, 12, 0, 0, 123456000, time.UTC)
	stub := &visitsHTTPStub{items: []domain.Visit{
		{ID: 9007199254740993, VisitedAt: at, UserAgent: "Browser/я"},
		{ID: 1, VisitedAt: at},
	}}
	handler := testHandler(&repositoryStub{})
	handler = NewHandler(nil, analytics.New(stub), nil, "https://sho.rt", testLogger())
	router := NewRouter(handler)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("GET", "/analytics/abc/visits?from=2024-02-29&to=2024-03-01&limit=1", nil))
	var body VisitsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || len(body.Items) != 1 || body.Items[0].ID != "9007199254740993" || !body.Items[0].VisitedAt.Equal(at) || body.Items[0].UserAgent != "Browser/я" || body.NextCursor == nil {
		t.Fatalf("status=%d body=%+v", response.Code, body)
	}
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest("HEAD", "/analytics/abc/visits", nil))
	if response.Code != 405 || response.Header().Get("Allow") != "GET" || stub.calls != 1 {
		t.Fatalf("status=%d calls=%d", response.Code, stub.calls)
	}
}
