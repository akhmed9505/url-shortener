package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/akhmed9505/url-shortener/internal/service/analytics"
)

type analyticsStub struct {
	err     error
	calls   int
	timeout time.Duration
}

func (s *analyticsStub) Summary(ctx context.Context, code string, p domain.AnalyticsPeriod) (domain.Analytics, error) {
	s.calls++
	deadline, _ := ctx.Deadline()
	s.timeout = time.Until(deadline)
	return domain.Analytics{Code: code, URL: "https://example.org", Period: p}, s.err
}
func TestAnalyticsHTTP(t *testing.T) {
	for _, tc := range []struct {
		query     string
		err       error
		status    int
		errorCode string
	}{
		{"?from=2024-02-29&to=2024-03-01", nil, 200, ""}, {"", nil, 200, ""},
		{"?from=2024-01-01", nil, 400, "invalid_period"}, {"?from=&to=", nil, 400, "invalid_period"},
		{"?from=2024-01-01&from=2024-01-02&to=2024-02-01", nil, 400, "invalid_period"},
		{"?from=%zz", nil, 400, "invalid_period"}, {"", domain.ErrLinkNotFound, 404, "link_not_found"},
		{"", domain.ErrStorageUnavailable, 503, "storage_unavailable"}, {"", context.DeadlineExceeded, 504, "request_timeout"},
		{"", errors.New("private database details"), 500, "internal_error"},
	} {
		t.Run(tc.query+tc.errorCode, func(t *testing.T) {
			stub := &analyticsStub{err: tc.err}
			handler := testHandler(&repositoryStub{})
			handler.analytics = analytics.New(stub)
			response := httptest.NewRecorder()
			NewRouter(handler).ServeHTTP(response, httptest.NewRequest("GET", "/analytics/abc"+tc.query, nil))
			if strings.Contains(response.Body.String(), "private database details") {
				t.Fatal("internal error leaked")
			}
			if response.Code != tc.status || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			if tc.status == 200 {
				var body AnalyticsResponse
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Code != "abc" || body.Daily == nil || body.Monthly == nil || body.UserAgents == nil || stub.timeout <= 0 || stub.timeout > 3*time.Second {
					t.Fatalf("body=%+v timeout=%v", body, stub.timeout)
				}
			} else {
				var body ErrorResponse
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Error.Code != tc.errorCode || body.RequestID == "" {
					t.Fatalf("body=%+v", body)
				}
			}
		})
	}
}

func (s *analyticsStub) ListVisits(ctx context.Context, _ string, _ domain.AnalyticsPeriod, _ *domain.VisitPosition, _ int) ([]domain.Visit, error) {
	s.calls++
	deadline, _ := ctx.Deadline()
	s.timeout = time.Until(deadline)
	return nil, s.err
}
