// Package http assembles the HTTP router and server.
package http

import (
	"log/slog"
	"net/http"

	"github.com/akhmed9505/url-shortener/internal/delivery/http/handler"
	"github.com/akhmed9505/url-shortener/internal/delivery/http/middleware"
	"github.com/akhmed9505/url-shortener/internal/delivery/http/response"
	"github.com/go-chi/chi/v5"
)

// NewRouter registers API and static routes with request logging and rate limits.
func NewRouter(h *handler.Handler, log *slog.Logger, limits *middleware.RateLimits) http.Handler {
	router := chi.NewRouter()
	router.Use(middleware.WithRequestID, middleware.WithAccessLog(log))
	router.Get("/", handler.Static("index.html", "text/html; charset=utf-8"))
	router.Get("/assets/app.css", handler.Static("assets/app.css", "text/css; charset=utf-8"))
	router.Get("/assets/app.js", handler.Static("assets/app.js", "text/javascript; charset=utf-8"))
	router.With(limits.Middleware(middleware.Creation)).Post("/shorten", h.Shorten)
	router.With(limits.Middleware(middleware.Analytics)).Get("/analytics/{code}", h.Analytics)
	router.With(limits.Middleware(middleware.Analytics)).Get("/analytics/{code}/visits", h.Visits)
	router.Get("/s/{code}", h.Redirect)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		response.Error(w, middleware.RequestID(r), http.StatusNotFound, "not_found", "Route not found", "")
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if router.Match(chi.NewRouteContext(), method, r.URL.Path) {
				w.Header().Add("Allow", method)
			}
		}
		response.Error(w, middleware.RequestID(r), http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", "")
	})
	router.Get("/health/ready", h.Ready)
	router.Get("/health/live", h.Live)
	return router
}
