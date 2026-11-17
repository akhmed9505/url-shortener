package handler_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	delivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	"github.com/akhmed9505/url-shortener/internal/delivery/http/middleware"
)

func TestAccessLogFieldsAndPrivacy(t *testing.T) {
	for _, tc := range []struct {
		method, path, pattern string
		status                int
	}{
		{"GET", "/s/private-code?token=private-query", "/s/{code}", 302},
		{"GET", "/analytics/private-code?from=private-query", "/analytics/{code}", 400},
		{"GET", "/analytics/private-code/visits?cursor=private-query", "/analytics/{code}/visits", 400},
		{"GET", "/private-path?token=private-query", "unmatched", 404},
		{"HEAD", "/s/private-code", "unmatched", 405},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			var logs bytes.Buffer
			handler := testHandler(&repositoryStub{originalURL: "https://private-target.example/secret"})
			log := slog.New(slog.NewJSONHandler(&logs, nil))
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader("private-body"))
			request.RemoteAddr = "203.0.113.9:1234"
			request.Header.Set("User-Agent", "private-agent")
			request.Header.Set("Authorization", "Bearer private-authorization")
			request.Header.Set("X-Request-ID", "private-request-id")
			response := httptest.NewRecorder()
			delivery.NewRouter(handler, log, middleware.NewRateLimits(time.Now)).ServeHTTP(response, request)
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body)
			}
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil {
				t.Fatal(err)
			}
			if len(entry) != 8 || entry["msg"] != "http_request" || entry["request_id"] != response.Header().Get("X-Request-ID") || entry["request_id"] == "" || entry["method"] != tc.method || entry["route_pattern"] != tc.pattern || entry["status"] != float64(tc.status) || entry["duration"].(float64) < 0 {
				t.Fatalf("entry=%v", entry)
			}
			for _, secret := range []string{"private-", "203.0.113.9", "Authorization", "token=", "cursor="} {
				if strings.Contains(logs.String(), secret) {
					t.Fatalf("sensitive data in access log: %q", secret)
				}
			}
		})
	}
}

func TestAccessLogIncludesRateLimitedRequests(t *testing.T) {
	var logs bytes.Buffer
	handler := testHandler(&repositoryStub{})
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	router := delivery.NewRouter(handler, log, middleware.NewRateLimits(newLimiterClock().Now))
	var response *httptest.ResponseRecorder
	for range 6 {
		response = rateLimitRequest(router, "POST", "/shorten", "192.0.2.1:1234")
	}
	if response.Code != 429 {
		t.Fatalf("status=%d", response.Code)
	}
	decoder := json.NewDecoder(&logs)
	for i := range 6 {
		var entry map[string]any
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal(err)
		}
		if entry["route_pattern"] != "/shorten" {
			t.Fatalf("entry=%v", entry)
		}
		if i == 5 && (entry["status"] != float64(429) || entry["request_id"] != response.Header().Get("X-Request-ID")) {
			t.Fatalf("rate limit log=%v", entry)
		}
	}
}
