//go:build integration

package integration

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	httpdelivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	rediscache "github.com/akhmed9505/url-shortener/internal/repository/redis"
	"github.com/akhmed9505/url-shortener/internal/service/analytics"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
)

// Bind real source IPs so HTTP integration tests exercise RemoteAddr without forwarded headers.
func clientFromIP(t *testing.T, ip net.IP) *http.Client {
	t.Helper()
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: ip}, Timeout: 5 * time.Second}
	transport := &http.Transport{DialContext: dialer.DialContext}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{
		Transport: transport, Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func TestPublicAPIRateLimits(t *testing.T) {
	pool, redis, link := cacheFixture(t)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	service := shortener.New(pgrepo.NewLinkRepository(pool), pgrepo.NewVisitRepository(pool), rediscache.NewLinkCache(redis, time.Hour), shortener.GenerateCode, 150*time.Millisecond, log)
	handler := httpdelivery.NewHandler(service, analytics.New(pgrepo.NewAnalyticsRepository(pool)), pool.Ping, "https://sho.rt", log)
	server := httptest.NewServer(httpdelivery.NewRouter(handler))
	t.Cleanup(server.Close)
	client := clientFromIP(t, net.IPv4(127, 0, 0, 1))
	request := func(method, path, body string) (int, http.Header, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", "203.0.113.1")
		req.Header.Set("X-Real-IP", "203.0.113.2")
		response, err := client.Do(req)
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
	assertLimited := func(method, path string) {
		t.Helper()
		status, headers, data := request(method, path, `{"url":"https://example.org"}`)
		retry, err := strconv.Atoi(headers.Get("Retry-After"))
		var body httpdelivery.ErrorResponse
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if status != 429 || err != nil || retry < 1 || body.Error.Code != "rate_limit_exceeded" || body.RequestID == "" || body.RequestID != headers.Get("X-Request-ID") {
			t.Fatalf("status=%d headers=%v body=%+v", status, headers, body)
		}
	}
	for range 5 {
		status, _, data := request("POST", "/shorten", `{"url":"https://example.org"}`)
		if status != 201 {
			t.Fatalf("creation status=%d body=%s", status, data)
		}
	}
	assertLimited("POST", "/shorten")
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM links").Scan(&count); err != nil || count != 6 {
		t.Fatalf("links after rejection=%d err=%v", count, err)
	}
	for i := range 10 {
		path := "/analytics/" + link.Code
		if i%2 == 0 {
			path += "/visits"
		}
		status, _, data := request("GET", path, "")
		if status != 200 {
			t.Fatalf("analytics status=%d body=%s", status, data)
		}
	}
	assertLimited("GET", "/analytics/"+link.Code)
	assertLimited("GET", "/analytics/"+link.Code+"/visits")
	for range 20 {
		status, headers, _ := request("GET", "/s/"+link.Code, "")
		if status != 302 || headers.Get("Location") != link.OriginalURL {
			t.Fatalf("redirect status=%d headers=%v", status, headers)
		}
	}
	if count := visitCount(t, pool); count != 20 {
		t.Fatalf("visits=%d", count)
	}
	other := clientFromIP(t, net.IPv4(127, 0, 0, 2))
	response, err := other.Get(server.URL + "/analytics/" + link.Code)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var summary httpdelivery.AnalyticsResponse
	if err := json.NewDecoder(response.Body).Decode(&summary); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || summary.TotalVisits != 20 {
		t.Fatalf("isolated client status=%d summary=%+v", response.StatusCode, summary)
	}
}
