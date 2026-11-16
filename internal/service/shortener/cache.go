package shortener

import (
	"context"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

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
