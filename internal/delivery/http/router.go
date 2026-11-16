package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func NewRouter(handler *Handler) http.Handler {
	router := chi.NewRouter()
	router.Use(withRequestID)
	router.Post("/shorten", handler.Shorten)
	router.Get("/analytics/{code}", handler.Analytics)
	router.Get("/s/{code}", handler.Redirect)
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "Route not found", "")
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			if router.Match(chi.NewRouteContext(), method, r.URL.Path) {
				w.Header().Add("Allow", method)
			}
		}
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "Method not allowed", "")
	})
	router.Get("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	return router
}
