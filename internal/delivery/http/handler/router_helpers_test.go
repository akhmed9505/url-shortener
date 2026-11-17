package handler_test

import (
	"io"
	"log/slog"
	"net/http"
	"time"

	delivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	"github.com/akhmed9505/url-shortener/internal/delivery/http/handler"
	"github.com/akhmed9505/url-shortener/internal/delivery/http/middleware"
)

func testLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }
func NewRouter(h *handler.Handler) http.Handler {
	return delivery.NewRouter(h, testLogger(), middleware.NewRateLimits(time.Now))
}
func newRouter(h *handler.Handler, limits *middleware.RateLimits) http.Handler {
	return delivery.NewRouter(h, testLogger(), limits)
}
