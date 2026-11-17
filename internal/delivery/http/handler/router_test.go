package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthLive(t *testing.T) {
	response := httptest.NewRecorder()
	NewRouter(testHandler(&repositoryStub{})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var body map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 1 || body["status"] != "ok" {
		t.Fatalf("body = %v, want status=ok", body)
	}
}

func TestHealthLiveRejectsUnsupportedMethods(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodHead, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			response := httptest.NewRecorder()
			NewRouter(testHandler(&repositoryStub{})).ServeHTTP(response, httptest.NewRequest(method, "/health/live", nil))
			if response.Code != http.StatusMethodNotAllowed {
				t.Errorf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
			}
			if got := response.Header().Get("Allow"); got != http.MethodGet {
				t.Errorf("Allow = %q, want GET", got)
			}
		})
	}
}

func TestUnknownRoute(t *testing.T) {
	response := httptest.NewRecorder()
	NewRouter(testHandler(&repositoryStub{})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/unknown", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
	assertAPIError(t, response, http.StatusNotFound, "not_found")
}

func TestShortenRejectsUnsupportedMethods(t *testing.T) {
	response := httptest.NewRecorder()
	NewRouter(testHandler(&repositoryStub{})).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/shorten", nil))
	assertAPIError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
	if got := response.Header().Get("Allow"); got != http.MethodPost {
		t.Fatalf("Allow = %q, want POST", got)
	}
}
