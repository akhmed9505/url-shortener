package middleware

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
			router.Use(WithRequestID, WithAccessLog(slog.New(slog.NewJSONHandler(&logs, nil))))
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
	WithAccessLog(slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private-panic") })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}

func TestAccessLogConcurrentRequests(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	router := chi.NewRouter()
	router.Use(WithRequestID, WithAccessLog(log))
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
