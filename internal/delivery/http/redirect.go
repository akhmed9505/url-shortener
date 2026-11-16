package http

import (
	"context"
	"net/http"
	"time"

	"github.com/akhmed9505/url-shortener/internal/config"
	"github.com/go-chi/chi/v5"
)

func (h *Handler) Redirect(w http.ResponseWriter, r *http.Request) {
	visitedAt := time.Now().UTC()
	ctx, cancel := context.WithTimeout(r.Context(), config.HTTPRequestTimeout)
	defer cancel()
	link, err := h.shortener.Resolve(ctx, chi.URLParam(r, "code"), r.UserAgent(), visitedAt)
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", link.OriginalURL)
	w.WriteHeader(http.StatusFound)
}
