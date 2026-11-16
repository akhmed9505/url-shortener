package domain

import "time"

type AnalyticsPeriod struct {
	From time.Time
	To   time.Time
}

type DailyVisits struct {
	Date  string
	Count int64
}

type MonthlyVisits struct {
	Month string
	Count int64
}

type UserAgentVisits struct {
	UserAgent string
	Count     int64
}

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
