package handler

import (
	"time"
)

// ShortenRequest holds the destination URL and optional custom alias.
type ShortenRequest struct {
	URL   string  `json:"url"`
	Alias *string `json:"alias,omitempty"`
}

// ShortenResponse holds the stored code, destination, public short URL and creation time.
type ShortenResponse struct {
	Code      string    `json:"code"`
	URL       string    `json:"url"`
	ShortURL  string    `json:"short_url"`
	CreatedAt time.Time `json:"created_at"`
}

// AnalyticsResponse holds link metadata, period boundaries and aggregate visit counts.
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

// DailyVisitsResponse holds a date and its visit count.
type DailyVisitsResponse struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

// MonthlyVisitsResponse holds a month and its visit count.
type MonthlyVisitsResponse struct {
	Month string `json:"month"`
	Count int64  `json:"count"`
}

// UserAgentVisitsResponse holds a raw User-Agent and its visit count.
type UserAgentVisitsResponse struct {
	UserAgent string `json:"user_agent"`
	Count     int64  `json:"count"`
}

// VisitsResponse holds link metadata and a cursor page of visits.
type VisitsResponse struct {
	Code       string              `json:"code"`
	From       string              `json:"from"`
	To         string              `json:"to"`
	Items      []VisitItemResponse `json:"items"`
	NextCursor *string             `json:"next_cursor"`
}

// VisitItemResponse holds a string visit ID, UTC timestamp and User-Agent.
type VisitItemResponse struct {
	ID        string    `json:"id"`
	VisitedAt time.Time `json:"visited_at"`
	UserAgent string    `json:"user_agent"`
}
