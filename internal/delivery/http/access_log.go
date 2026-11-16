package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func withAccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			writer := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			completed := false
			defer func() {
				status := writer.Status()
				if status == 0 {
					status = http.StatusOK
					if !completed {
						status = http.StatusInternalServerError
					}
				}
				pattern := "unmatched"
				if route := chi.RouteContext(r.Context()); route != nil && route.RoutePattern() != "" {
					pattern = route.RoutePattern()
				}
				log.InfoContext(r.Context(), "http_request",
					"request_id", requestID(r), "method", r.Method, "route_pattern", pattern,
					"status", status, "duration", time.Since(started))
			}()
			next.ServeHTTP(writer, r)
			completed = true
		})
	}
}
