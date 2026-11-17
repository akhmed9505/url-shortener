package shortener

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

// Resolve finds a link and records its visit on a best-effort basis.
func (s *Service) Resolve(ctx context.Context, code, userAgent string, visitedAt time.Time) (domain.Link, error) {
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	if !domain.ValidCode(code) {
		return domain.Link{}, &ValidationError{Field: "code", Message: "Invalid short code"}
	}
	link, err := s.findLink(ctx, code)
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

func (s *Service) findLink(ctx context.Context, code string) (domain.Link, error) {
	link, hit, err := s.cache.Get(ctx, code)
	if err != nil {
		s.log.WarnContext(ctx, "cache_error", "operation", "get")
	} else if hit {
		if link.ID > 0 && link.Code == code && ValidateURL(link.OriginalURL) == nil {
			return link, nil
		}
		s.log.WarnContext(ctx, "cache_error", "operation", "decode")
	}
	link, err = s.repository.FindByCode(ctx, code)
	if err != nil {
		return domain.Link{}, err
	}
	if err := s.cache.Set(ctx, link); err != nil {
		s.log.WarnContext(ctx, "cache_error", "operation", "set")
	}
	return link, nil
}
