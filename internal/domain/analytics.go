// Package domain defines link and analytics models and errors.
package domain

import "time"

// AnalyticsPeriod defines a half-open UTC interval from From to To.
type AnalyticsPeriod struct {
	From time.Time
	To   time.Time
}

// DailyVisits holds a UTC date and its visit count.
type DailyVisits struct {
	Date  string
	Count int64
}

// MonthlyVisits holds a UTC month and its visit count.
type MonthlyVisits struct {
	Month string
	Count int64
}

// UserAgentVisits holds a raw User-Agent and its visit count.
type UserAgentVisits struct {
	UserAgent string
	Count     int64
}

// Analytics provides summaries and visit pages to HTTP handlers.
type Analytics struct {
	Code                  string
	URL                   string
	TotalVisits           int64
	PeriodVisits          int64
	Period                AnalyticsPeriod
	Daily                 []DailyVisits
	Monthly               []MonthlyVisits
	UserAgents            []UserAgentVisits
	OtherUserAgentsVisits int64
}
