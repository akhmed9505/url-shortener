package http

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
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
			handler.log = slog.New(slog.NewJSONHandler(&logs, nil))
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader("private-body"))
			request.RemoteAddr = "203.0.113.9:1234"
			request.Header.Set("User-Agent", "private-agent")
			request.Header.Set("Authorization", "Bearer private-authorization")
			request.Header.Set("X-Request-ID", "private-request-id")
			response := httptest.NewRecorder()
			NewRouter(handler).ServeHTTP(response, request)
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

func TestAccessLogResponseStatusAndFlushing(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
	}{
		{"empty", func(http.ResponseWriter, *http.Request) {}, 200},
		{"implicit", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }, 200},
		{"first status", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(201)
			w.WriteHeader(500)
		}, 201},
		{"early hints", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(103)
			w.WriteHeader(204)
		}, 204},
		{"flush", func(w http.ResponseWriter, _ *http.Request) {
			if err := http.NewResponseController(w).Flush(); err != nil {
				panic(err)
			}
		}, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			router := chi.NewRouter()
			router.Use(withRequestID, withAccessLog(slog.New(slog.NewJSONHandler(&logs, nil))))
			router.Get("/test", tc.handler)
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test", nil))
			var entry map[string]any
			if err := json.Unmarshal(logs.Bytes(), &entry); err != nil || entry["status"] != float64(tc.status) {
				t.Fatalf("log=%s err=%v", logs.String(), err)
			}
		})
	}
}

func TestAccessLogPanicPreservesPanicAndHidesValue(t *testing.T) {
	var logs bytes.Buffer
	defer func() {
		if value := recover(); value != "private-panic" {
			t.Errorf("panic=%v", value)
		}
		var entry map[string]any
		if err := json.Unmarshal(logs.Bytes(), &entry); err != nil || entry["status"] != float64(500) || strings.Contains(logs.String(), "private-panic") {
			t.Errorf("log=%s err=%v", logs.String(), err)
		}
	}()
	withAccessLog(slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private-panic") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestAccessLogConcurrentRequests(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(withRequestID, withAccessLog(log))
	router.Get("/test/{code}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	var workers sync.WaitGroup
	for range 64 {
		workers.Go(func() {
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/test/private-code?q=secret", nil))
		})
	}
	workers.Wait()
	decoder := json.NewDecoder(&logs)
	ids := make(map[string]bool)
	for {
		var entry struct {
			RequestID string `json:"request_id"`
			Pattern   string `json:"route_pattern"`
		}
		err := decoder.Decode(&entry)
		if err == io.EOF {
			break
		}
		if err != nil || entry.Pattern != "/test/{code}" || entry.RequestID == "" || ids[entry.RequestID] {
			t.Fatalf("entry=%+v err=%v", entry, err)
		}
		ids[entry.RequestID] = true
	}
	if len(ids) != 64 {
		t.Fatalf("entries=%d", len(ids))
	}
}

func TestAccessLogIncludesRateLimitedRequests(t *testing.T) {
	var logs bytes.Buffer
	handler := testHandler(&repositoryStub{})
	handler.log = slog.New(slog.NewJSONHandler(&logs, nil))
	router := newRouter(handler, newRateLimits(newLimiterClock().Now))
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
