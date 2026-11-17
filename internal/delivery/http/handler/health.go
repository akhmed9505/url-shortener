package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/akhmed9505/url-shortener/internal/delivery/http/middleware"
	httpresponse "github.com/akhmed9505/url-shortener/internal/delivery/http/response"
)

// Ready checks PostgreSQL readiness within a one-second deadline.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if h.ready == nil || h.ready(ctx) != nil {
		httpresponse.Error(w, middleware.RequestID(r), http.StatusServiceUnavailable, "not_ready", "Service is not ready", "")
		return
	}
	httpresponse.JSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "ok"})
}

// Live reports liveness independently of external dependencies.
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
}
