package http

import (
	"context"
	"net/http"
	"time"
)

func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	if h.ready == nil || h.ready(ctx) != nil {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "Service is not ready", "")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{Status: "ok"})
}
