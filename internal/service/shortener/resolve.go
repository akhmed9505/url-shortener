package shortener

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

func (s *Service) Resolve(ctx context.Context, code, userAgent string, visitedAt time.Time) (domain.Link, error) {
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	if !validCode(code) {
		return domain.Link{}, &ValidationError{Field: "code", Message: "Invalid short code"}
	}
	link, err := s.repository.FindByCode(ctx, code)
	if err != nil {
		return domain.Link{}, err
	}
	visit := domain.Visit{LinkID: link.ID, VisitedAt: visitedAt.UTC(), UserAgent: normalizeUserAgent(userAgent)}
	writeCtx, cancel := context.WithTimeout(ctx, s.writeTimeout)
	defer cancel()
	if err := s.visits.Record(writeCtx, visit); err != nil {
		reason := "storage_error"
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "timeout"
		} else if errors.Is(err, context.Canceled) {
			reason = "canceled"
		}
		s.log.ErrorContext(ctx, "visit_write_failed", "reason", reason)
	}
	return link, nil
}

func normalizeUserAgent(raw string) string {
	normalized := strings.ToValidUTF8(raw, "\uFFFD")
	// PostgreSQL text cannot contain NUL even though it is valid UTF-8.
	normalized = strings.ReplaceAll(normalized, "\x00", "\uFFFD")
	if len(normalized) <= 1024 {
		return normalized
	}
	end := 1024
	for !utf8.RuneStart(normalized[end]) {
		end--
	}
	return normalized[:end]
}
