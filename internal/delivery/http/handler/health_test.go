package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "github.com/akhmed9505/url-shortener/internal/delivery/http/handler"
)

func TestReadiness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"available", nil, 200},
		{"unavailable", errors.New("postgres://user:private-password@private-host/db"), 503},
		{"timeout", context.DeadlineExceeded, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			ready := func(ctx context.Context) error {
				calls++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > time.Second {
					t.Fatalf("unexpected readiness deadline: %v", deadline)
				}
				return tc.err
			}
			response := httptest.NewRecorder()
			NewRouter(NewHandler(nil, nil, ready, "https://sho.rt", testLogger())).ServeHTTP(response, httptest.NewRequest("GET", "/health/ready", nil))
			if response.Code != tc.status || calls != 1 || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("status=%d calls=%d headers=%v", response.Code, calls, response.Header())
			}
			if tc.status == 200 {
				var body map[string]string
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || len(body) != 1 || body["status"] != "ok" {
					t.Fatalf("body=%v err=%v", body, err)
				}
			} else {
				assertAPIError(t, response, 503, "not_ready")
				if strings.Contains(response.Body.String(), "private-") {
					t.Fatal("readiness leaked connection details")
				}
			}
		})
	}
}

func TestReadinessHonorsParentDeadline(t *testing.T) {
	ready := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("GET", "/health/ready", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	NewRouter(NewHandler(nil, nil, ready, "https://sho.rt", testLogger())).ServeHTTP(response, request)
	assertAPIError(t, response, 503, "not_ready")
}

func TestLivenessAndUnsupportedMethodsSkipReadiness(t *testing.T) {
	ready := func(context.Context) error {
		t.Error("unexpected readiness call")
		return errors.New("unavailable")
	}
	router := NewRouter(NewHandler(nil, nil, ready, "https://sho.rt", testLogger()))
	for _, tc := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/health/live", 200}, {"HEAD", "/health/ready", 405}, {"POST", "/health/ready", 405},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(tc.method, tc.path, nil))
		if response.Code != tc.status {
			t.Fatalf("%s %s status=%d", tc.method, tc.path, response.Code)
		}
	}
}
