package http

import (
	"context"
	"net/http"
	"net/url"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/go-chi/chi/v5"
)

type AnalyticsResponse struct {
	Code                  string                    `json:"code"`
	URL                   string                    `json:"url"`
	TotalVisits           int64                     `json:"total_visits"`
	PeriodVisits          int64                     `json:"period_visits"`
	From                  string                    `json:"from"`
	To                    string                    `json:"to"`
	Daily                 []DailyVisitsResponse     `json:"daily"`
	Monthly               []MonthlyVisitsResponse   `json:"monthly"`
	UserAgents            []UserAgentVisitsResponse `json:"user_agents"`
	OtherUserAgentsVisits int64                     `json:"other_user_agents_visits"`
}
type DailyVisitsResponse struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}
type MonthlyVisitsResponse struct {
	Month string `json:"month"`
	Count int64  `json:"count"`
}
type UserAgentVisitsResponse struct {
	UserAgent string `json:"user_agent"`
	Count     int64  `json:"count"`
}

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
	writeJSON(w, http.StatusOK, response)
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
