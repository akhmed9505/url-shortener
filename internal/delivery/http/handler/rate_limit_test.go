package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/akhmed9505/url-shortener/internal/delivery/http/handler"
	"github.com/akhmed9505/url-shortener/internal/delivery/http/middleware"
	. "github.com/akhmed9505/url-shortener/internal/delivery/http/response"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
)

type limiterClock struct {
	mu   sync.Mutex
	time time.Time
}

func newLimiterClock() *limiterClock {
	return &limiterClock{time: time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)}
}

func (c *limiterClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.time
}

func (c *limiterClock) Advance(duration time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.time = c.time.Add(duration)
}

func rateLimitRequest(router http.Handler, method, path, remoteAddr string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = remoteAddr
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestRateLimitRoutesAndSharedAnalytics(t *testing.T) {
	clock := newLimiterClock()
	limits := middleware.NewRateLimits(clock.Now)
	handler := testHandler(&repositoryStub{})

	router := newRouter(handler, limits)
	remote := "192.0.2.1:1234"
	for range 5 {
		if response := rateLimitRequest(router, "POST", "/shorten", remote); response.Code != 400 {
			t.Fatalf("creation burst status=%d", response.Code)
		}
	}
	response := rateLimitRequest(router, "POST", "/shorten", remote)
	assertRateLimitResponse(t, response, "6")
	for i := range 10 {
		path := "/analytics/abc"
		if i%2 == 0 {
			path += "/visits"
		}
		if response := rateLimitRequest(router, "GET", path, remote); response.Code != 200 {
			t.Fatalf("analytics burst status=%d body=%s", response.Code, response.Body)
		}
	}
	assertRateLimitResponse(t, rateLimitRequest(router, "GET", "/analytics/abc", remote), "1")
	assertRateLimitResponse(t, rateLimitRequest(router, "GET", "/analytics/other/visits", remote), "1")
	clock.Advance(time.Second)
	if response := rateLimitRequest(router, "GET", "/analytics/abc/visits", remote); response.Code != 200 {
		t.Fatalf("refilled analytics status=%d", response.Code)
	}
	assertRateLimitResponse(t, rateLimitRequest(router, "GET", "/analytics/abc", remote), "1")
	assertRateLimitResponse(t, rateLimitRequest(router, "POST", "/shorten", remote), "5")
	if response := rateLimitRequest(router, "GET", "/analytics/abc", "192.0.2.2:1234"); response.Code != 200 {
		t.Fatalf("separate IP status=%d", response.Code)
	}
}

func assertRateLimitResponse(t *testing.T, response *httptest.ResponseRecorder, retry string) {
	t.Helper()
	var body ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != 429 || response.Header().Get("Retry-After") != retry || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != "application/json" || body.Error.Code != "rate_limit_exceeded" || body.RequestID == "" || response.Header().Get("X-Request-ID") != body.RequestID {
		t.Fatalf("status=%d headers=%v body=%+v", response.Code, response.Header(), body)
	}
}

func TestRateLimitsBypassRedirectsAndHealthAtCapacity(t *testing.T) {
	limits := middleware.NewRateLimits(newLimiterClock().Now)
	for i := range 10_000 {
		rateLimitRequest(limits.Middleware(middleware.Creation)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), "POST", "/", netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}).String()+":1")
	}
	visits := &visitStub{}
	handler := testHandler(&repositoryStub{})
	handler = NewHandler(shortener.New(&repositoryStub{}, visits, missingCache{}, shortener.GenerateCode, 150*time.Millisecond, testLogger()), nil, nil, "https://sho.rt", testLogger())
	router := newRouter(handler, limits)
	for range 100 {
		if response := rateLimitRequest(router, "GET", "/s/abc", "192.0.2.1:1234"); response.Code != 302 {
			t.Fatalf("redirect status=%d body=%s", response.Code, response.Body)
		}
		if response := rateLimitRequest(router, "GET", "/health/live", "192.0.2.1:1234"); response.Code != 200 {
			t.Fatalf("health status=%d", response.Code)
		}
	}
	if len(visits.visits) != 100 {
		t.Fatalf("visits=%d tracked IPs=%d", len(visits.visits), 10_000)
	}
	assertRateLimitResponse(t, rateLimitRequest(router, "POST", "/shorten", "192.0.2.1:1234"), "600")
	assertRateLimitResponse(t, rateLimitRequest(router, "GET", "/analytics/abc", "192.0.2.1:1234"), "600")
}
