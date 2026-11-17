// Package logger configures structured application logging.
package logger

import (
	"io"
	"log/slog"
)

// New creates a JSON slog logger writing at the selected level.
func New(output io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}))
}
