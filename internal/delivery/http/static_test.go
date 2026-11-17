package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedInterface(t *testing.T) {
	// Embedded assets must work even when the process starts outside the repository.
	t.Chdir(t.TempDir())
	router := NewRouter(testHandler(&repositoryStub{}))
	for _, tc := range []struct{ path, contentType, contains string }{
		{"/", "text/html; charset=utf-8", `<html lang="ru">`},
		{"/assets/app.css", "text/css; charset=utf-8", "overflow-x: auto"},
		{"/assets/app.js", "text/javascript; charset=utf-8", "next_cursor"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body)
			}
			if response.Header().Get("Content-Type") != tc.contentType {
				t.Errorf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
			if !strings.Contains(response.Body.String(), tc.contains) {
				t.Errorf("missing expected content %q", tc.contains)
			}
			if response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("missing nosniff")
			}
			if response.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Error("missing referrer policy")
			}
			if !strings.Contains(response.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
				t.Error("missing CSP")
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Error("missing request ID")
			}
			if response.Header().Get("Cache-Control") != "no-cache" {
				t.Error("assets must revalidate after updates")
			}
		})
	}
}

func TestStaticRoutesDoNotExposeOtherFiles(t *testing.T) {
	router := NewRouter(testHandler(&repositoryStub{}))
	for _, path := range []string{"/index.html", "/assets/", "/assets/missing.js", "/web.go", "/.env", "/go.mod", "/assets/../go.mod", "/assets/%2e%2e/go.mod", "/anything"} {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			assertAPIError(t, response, http.StatusNotFound, "not_found")
		})
	}
	for _, path := range []string{"/", "/assets/app.css", "/assets/app.js"} {
		for _, method := range []string{http.MethodPost, http.MethodHead} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(method, path, nil))
			assertAPIError(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
			if response.Header().Get("Allow") != http.MethodGet {
				t.Errorf("Allow = %q", response.Header().Get("Allow"))
			}
		}
	}
}
