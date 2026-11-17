// Package handler translates HTTP requests into service calls.
package handler

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	httpresponse "github.com/akhmed9505/url-shortener/internal/delivery/http/response"
	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/go-chi/chi/v5"
)

// Analytics serves aggregate visit statistics for the requested period.
func (h *Handler) Analytics(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	query, err := analyticsQuery(r.URL.RawQuery)
	if err != nil {
		h.operationError(w, r, err)
		return
	}

	summary, err := h.analytics.Summary(ctx, chi.URLParam(r, "code"), query.Get("from"), query.Get("to"), time.Now())
	if err != nil {
		h.operationError(w, r, err)
		return
	}
	response := AnalyticsResponse{
		Code: summary.Code, URL: summary.URL, TotalVisits: summary.TotalVisits, PeriodVisits: summary.PeriodVisits,
		From: summary.Period.From.Format(time.DateOnly), To: summary.Period.To.Format(time.DateOnly),
		Daily: make([]DailyVisitsResponse, 0, len(summary.Daily)), Monthly: make([]MonthlyVisitsResponse, 0, len(summary.Monthly)),
		UserAgents: make([]UserAgentVisitsResponse, 0, len(summary.UserAgents)), OtherUserAgentsVisits: summary.OtherUserAgentsVisits,
	}
	for _, day := range summary.Daily {
		response.Daily = append(response.Daily, DailyVisitsResponse{Date: day.Date, Count: day.Count})
	}
	for _, month := range summary.Monthly {
		response.Monthly = append(response.Monthly, MonthlyVisitsResponse{Month: month.Month, Count: month.Count})
	}
	for _, agent := range summary.UserAgents {
		response.UserAgents = append(response.UserAgents, UserAgentVisitsResponse{UserAgent: agent.UserAgent, Count: agent.Count})
	}
	httpresponse.JSON(w, http.StatusOK, response)
}

func analyticsQuery(raw string) (url.Values, error) {
	query, err := url.ParseQuery(raw)
	if err != nil {
		return nil, &domain.ValidationError{Field: "period", Message: "Invalid query parameters"}
	}
	from, to := query["from"], query["to"]
	if len(from) > 1 || len(to) > 1 || (len(from) == 1 && from[0] == "") || (len(to) == 1 && to[0] == "") {
		return nil, &domain.ValidationError{Field: "period", Message: "from and to must each contain one non-empty date"}
	}
	return query, nil
}

// Visits serves a cursor page of recorded link visits.
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
	httpresponse.JSON(w, http.StatusOK, response)
}
