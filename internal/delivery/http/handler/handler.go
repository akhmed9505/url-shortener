package handler

import (
	"context"
	"log/slog"
)

// Handler serves link, analytics and health requests using explicit dependencies.
type Handler struct {
	ready     func(context.Context) error
	analytics Analytics
	shortener Shortener
	baseURL   string
	log       *slog.Logger
}

// NewHandler creates HTTP handlers with the supplied services, readiness check and public origin.
func NewHandler(service Shortener, analyticsService Analytics, ready func(context.Context) error, baseURL string, log *slog.Logger) *Handler {
	return &Handler{ready: ready, analytics: analyticsService, shortener: service, baseURL: baseURL, log: log}
}
