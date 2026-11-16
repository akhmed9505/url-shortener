package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/service/analytics"
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

func TestRateLimitsBurstAndRefill(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bucket   rateBucket
		burst    int
		interval time.Duration
	}{
		{"creation", creationBucket, 5, 6 * time.Second},
		{"analytics", analyticsBucket, 10, time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clock := newLimiterClock()
			limits := newRateLimits(clock.Now)
			ip := netip.MustParseAddr("192.0.2.1")
			for range tc.burst {
				if retry := limits.allow(ip, tc.bucket); retry != 0 {
					t.Fatalf("initial burst rejected: retry=%d", retry)
				}
			}
			for range 100 {
				if retry := limits.allow(ip, tc.bucket); retry != int(tc.interval/time.Second) {
					t.Fatalf("retry=%d interval=%v", retry, tc.interval)
				}
			}
			clock.Advance(tc.interval / 2)
			if retry := limits.allow(ip, tc.bucket); retry == 0 {
				t.Fatal("fractional token admitted")
			}
			clock.Advance(tc.interval / 2)
			if retry := limits.allow(ip, tc.bucket); retry != 0 {
				t.Fatalf("refilled token rejected: retry=%d", retry)
			}
			if retry := limits.allow(ip, tc.bucket); retry == 0 {
				t.Fatal("extra token admitted")
			}
			clock.Advance(time.Minute)
			for range tc.burst {
				if retry := limits.allow(ip, tc.bucket); retry != 0 {
					t.Fatalf("refilled burst rejected: retry=%d", retry)
				}
			}
			if retry := limits.allow(ip, tc.bucket); retry == 0 {
				t.Fatal("tokens exceeded burst capacity")
			}
		})
	}
}

func TestRateLimitsClientAndBucketIsolation(t *testing.T) {
	limits := newRateLimits(newLimiterClock().Now)
	first, second := netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.2")
	for range 5 {
		if limits.allow(first, creationBucket) != 0 {
			t.Fatal("creation burst rejected")
		}
	}
	if limits.allow(first, creationBucket) == 0 || limits.allow(second, creationBucket) != 0 {
		t.Fatal("client buckets are not isolated")
	}
	for range 10 {
		if limits.allow(first, analyticsBucket) != 0 {
			t.Fatal("creation consumed analytics tokens")
		}
	}
	if limits.allow(first, analyticsBucket) == 0 || limits.allow(second, analyticsBucket) != 0 || len(limits.entries) != 2 {
		t.Fatal("analytics client isolation or shared IP accounting failed")
	}
}

func TestRateLimitsIdleCleanup(t *testing.T) {
	clock := newLimiterClock()
	limits := newRateLimits(clock.Now)
	first := netip.MustParseAddr("192.0.2.1")
	second := netip.MustParseAddr("192.0.2.2")
	third := netip.MustParseAddr("192.0.2.3")
	limits.allow(first, creationBucket)
	clock.Advance(time.Minute)
	limits.allow(second, analyticsBucket)
	clock.Advance(time.Minute)
	limits.allow(first, analyticsBucket)
	clock.Advance(9 * time.Minute)
	limits.allow(third, creationBucket)
	if limits.entries[second] != nil || limits.entries[first] == nil || len(limits.entries) != 2 || limits.activity.Len() != 2 {
		t.Fatal("idle cleanup did not respect latest activity across both buckets")
	}
	clock.Advance(time.Minute)
	limits.allow(third, creationBucket)
	if limits.entries[first] != nil || len(limits.entries) != 1 || limits.activity.Len() != 1 {
		t.Fatal("entry was not removed at the idle boundary")
	}
	for range 5 {
		if limits.allow(first, creationBucket) != 0 {
			t.Fatal("expired entry did not receive a fresh bucket")
		}
	}
}

func TestRateLimitsRejectedRequestsRefreshActivity(t *testing.T) {
	clock := newLimiterClock()
	limits := newRateLimits(clock.Now)
	ip := netip.MustParseAddr("192.0.2.1")
	for range 5 {
		limits.allow(ip, creationBucket)
	}
	clock.Advance(time.Second)
	if limits.allow(ip, creationBucket) == 0 {
		t.Fatal("expected exhausted bucket")
	}
	clock.Advance(ipIdleTTL - 500*time.Millisecond)
	other := netip.MustParseAddr("192.0.2.2")
	limits.allow(other, creationBucket)
	if limits.entries[ip] == nil {
		t.Fatal("rejected request was ignored as activity")
	}
	clock.Advance(500 * time.Millisecond)
	limits.allow(other, creationBucket)
	if limits.entries[ip] != nil {
		t.Fatal("expired entry was retained")
	}
}

func TestRateLimitsCapacity(t *testing.T) {
	clock := newLimiterClock()
	limits := newRateLimits(clock.Now)
	for i := range maxTrackedIPs {
		ip := netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)})
		if limits.allow(ip, creationBucket) != 0 {
			t.Fatalf("IP %d rejected before capacity", i)
		}
	}
	unknown := netip.MustParseAddr("192.0.2.1")
	for range 100 {
		if retry := limits.allow(unknown, analyticsBucket); retry != 600 {
			t.Fatalf("capacity retry=%d", retry)
		}
	}
	if len(limits.entries) != maxTrackedIPs || limits.activity.Len() != maxTrackedIPs || limits.entries[unknown] != nil {
		t.Fatal("capacity rejection changed tracked entries")
	}
	clock.Advance(time.Minute)
	existing := netip.MustParseAddr("10.0.0.0")
	if limits.allow(existing, analyticsBucket) != 0 {
		t.Fatal("existing IP rejected at capacity")
	}
	if retry := limits.allow(unknown, analyticsBucket); retry != 540 {
		t.Fatalf("capacity retry did not account for idle time: %d", retry)
	}
	clock.Advance(9 * time.Minute)
	if limits.allow(unknown, analyticsBucket) != 0 || len(limits.entries) != 2 || limits.activity.Len() != 2 || limits.entries[existing] == nil {
		t.Fatal("idle capacity was not reclaimed without evicting active IP")
	}
}

func TestRateLimitsConcurrentRequests(t *testing.T) {
	limits := newRateLimits(newLimiterClock().Now)
	var creation, analytics atomic.Int64
	creationHandler := withRequestID(limits.middleware(creationBucket)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		creation.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})))
	analyticsHandler := withRequestID(limits.middleware(analyticsBucket)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		analytics.Add(1)
		w.WriteHeader(http.StatusNoContent)
	})))
	var workers sync.WaitGroup
	start := make(chan struct{})
	for i := range 256 {
		workers.Go(func() {
			<-start
			handler, retry := creationHandler, "6"
			if i%2 != 0 {
				handler, retry = analyticsHandler, "1"
			}
			response := rateLimitRequest(handler, "GET", "/", "192.0.2.1:1234")
			if response.Code != http.StatusNoContent {
				assertRateLimitResponse(t, response, retry)
			}
		})
	}
	close(start)
	workers.Wait()
	if creation.Load() != 5 || analytics.Load() != 10 || len(limits.entries) != 1 {
		t.Fatalf("creation=%d analytics=%d entries=%d", creation.Load(), analytics.Load(), len(limits.entries))
	}
}

func TestRateLimitsConcurrentCapacity(t *testing.T) {
	limits := newRateLimits(newLimiterClock().Now)
	for i := range maxTrackedIPs - 2 {
		limits.allow(netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}), creationBucket)
	}
	var admitted atomic.Int64
	var workers sync.WaitGroup
	for i := range 128 {
		workers.Go(func() {
			ip := netip.AddrFrom4([4]byte{192, 0, 2, byte(i)})
			if limits.allow(ip, analyticsBucket) == 0 {
				admitted.Add(1)
			}
		})
	}
	workers.Wait()
	if admitted.Load() != 2 || len(limits.entries) != maxTrackedIPs || limits.activity.Len() != maxTrackedIPs {
		t.Fatalf("admitted=%d entries=%d activity=%d", admitted.Load(), len(limits.entries), limits.activity.Len())
	}
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
	limits := newRateLimits(clock.Now)
	handler := testHandler(&repositoryStub{})
	handler.analytics = analytics.New(&analyticsStub{})
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

func TestRateLimitsIgnoreForwardedHeadersAndPorts(t *testing.T) {
	limits := newRateLimits(newLimiterClock().Now)
	var called int
	router := withRequestID(limits.middleware(creationBucket)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called++
		w.WriteHeader(http.StatusNoContent)
	})))
	for i := range 20 {
		request := httptest.NewRequest("POST", "/shorten", nil)
		request.RemoteAddr = fmt.Sprintf("192.0.2.1:%d", 1000+i)
		if i%2 == 0 {
			request.RemoteAddr = fmt.Sprintf("[::ffff:192.0.2.1]:%d", 1000+i)
		}
		request.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		request.Header.Set("X-Real-IP", fmt.Sprintf("198.51.100.%d", i))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if i < 5 {
			if response.Code != 204 {
				t.Fatalf("initial request status=%d", response.Code)
			}
		} else {
			assertRateLimitResponse(t, response, "6")
		}
	}
	if called != 5 || len(limits.entries) != 1 {
		t.Fatalf("handler calls=%d tracked IPs=%d", called, len(limits.entries))
	}
}

func TestRateLimitsBypassRedirectsAndHealthAtCapacity(t *testing.T) {
	limits := newRateLimits(newLimiterClock().Now)
	for i := range maxTrackedIPs {
		limits.allow(netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}), creationBucket)
	}
	visits := &visitStub{}
	handler := testHandler(&repositoryStub{})
	handler.shortener = shortener.New(&repositoryStub{}, visits, missingCache{}, shortener.GenerateCode, 150*time.Millisecond, handler.log)
	router := newRouter(handler, limits)
	for range 100 {
		if response := rateLimitRequest(router, "GET", "/s/abc", "192.0.2.1:1234"); response.Code != 302 {
			t.Fatalf("redirect status=%d body=%s", response.Code, response.Body)
		}
		if response := rateLimitRequest(router, "GET", "/health/live", "192.0.2.1:1234"); response.Code != 200 {
			t.Fatalf("health status=%d", response.Code)
		}
	}
	if len(visits.visits) != 100 || len(limits.entries) != maxTrackedIPs {
		t.Fatalf("visits=%d tracked IPs=%d", len(visits.visits), len(limits.entries))
	}
	assertRateLimitResponse(t, rateLimitRequest(router, "POST", "/shorten", "192.0.2.1:1234"), "600")
	assertRateLimitResponse(t, rateLimitRequest(router, "GET", "/analytics/abc", "192.0.2.1:1234"), "600")
}

func TestRemoteIP(t *testing.T) {
	for _, tc := range []struct{ remote, want string }{
		{"192.0.2.1:1234", "192.0.2.1"}, {"192.0.2.1", "192.0.2.1"},
		{"[::ffff:192.0.2.1]:1234", "192.0.2.1"}, {"[2001:db8::1]:443", "2001:db8::1"},
		{"2001:db8::1", "2001:db8::1"}, {"[fe80::1%eth0]:443", "fe80::1"},
		{"bad:address", "invalid IP"}, {"", "invalid IP"},
	} {
		if got := remoteIP(tc.remote).String(); got != tc.want {
			t.Errorf("remoteIP(%q)=%q want=%q", tc.remote, got, tc.want)
		}
	}
}

func TestRateLimitRetryAfterRounding(t *testing.T) {
	for _, tc := range []struct {
		elapsed time.Duration
		want    string
	}{
		{time.Nanosecond, "6"}, {time.Second, "5"}, {1500 * time.Millisecond, "5"}, {6*time.Second - time.Nanosecond, "1"},
	} {
		t.Run(tc.elapsed.String(), func(t *testing.T) {
			clock := newLimiterClock()
			limits := newRateLimits(clock.Now)
			ip := netip.MustParseAddr("192.0.2.1")
			for range 5 {
				limits.allow(ip, creationBucket)
			}
			clock.Advance(tc.elapsed)
			handler := withRequestID(limits.middleware(creationBucket)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("request admitted before refill") })))
			assertRateLimitResponse(t, rateLimitRequest(handler, "POST", "/shorten", "192.0.2.1:9999"), tc.want)
		})
	}
}
