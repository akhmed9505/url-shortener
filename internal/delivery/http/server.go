package http

import (
	"log/slog"
	"net/http"

	"github.com/akhmed9505/url-shortener/internal/config"
)

// NewServer creates an HTTP server with configured timeouts and structured error logging.
func NewServer(cfg config.HTTP, handler http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
	}
}
