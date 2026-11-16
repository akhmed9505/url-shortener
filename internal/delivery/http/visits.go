package http

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/go-chi/chi/v5"
)

type VisitsResponse struct {
	Code       string              `json:"code"`
	From       string              `json:"from"`
	To         string              `json:"to"`
	Items      []VisitItemResponse `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

type VisitItemResponse struct {
	ID        string    `json:"id"`
	VisitedAt time.Time `json:"visited_at"`
	UserAgent string    `json:"user_agent"`
}

func (h *Handler) Visits(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	query, err := analyticsQuery(r.URL.RawQuery)
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	if cursors, present := query["cursor"]; present && (len(cursors) != 1 || cursors[0] == "") {
		h.operationError(w, r, &domain.ValidationError{Field: "cursor", Message: "Invalid cursor"})
		return
	}
	limit := 20
	if values, present := query["limit"]; present {
		if len(values) != 1 {
			h.operationError(w, r, &domain.ValidationError{Field: "limit", Message: "Expected one limit value"})
			return
		}
		limit, err = strconv.Atoi(values[0])
		if err != nil || limit < 1 || limit > 100 {
			h.operationError(w, r, &domain.ValidationError{Field: "limit", Message: "limit must be between 1 and 100"})
			return
		}
	}
	page, err := h.analytics.Visits(ctx, chi.URLParam(r, "code"), query.Get("from"), query.Get("to"), limit, query.Get("cursor"), time.Now())
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	response := VisitsResponse{
		Code: page.Code, From: page.Period.From.Format(time.DateOnly), To: page.Period.To.Format(time.DateOnly),
		Items: make([]VisitItemResponse, 0, len(page.Items)), NextCursor: page.NextCursor,
	}
	for _, visit := range page.Items {
		response.Items = append(response.Items, VisitItemResponse{
			ID: strconv.FormatInt(visit.ID, 10), VisitedAt: visit.VisitedAt.UTC(), UserAgent: visit.UserAgent,
		})
	}
	writeJSON(w, http.StatusOK, response)
}
