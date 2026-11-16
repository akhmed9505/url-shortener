package shortener

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

var ErrCodeGenerationExhausted = errors.New("short code generation attempts exhausted")

type LinkRepository interface {
	Create(ctx context.Context, code, originalURL string) (domain.Link, error)
	FindByCode(ctx context.Context, code string) (domain.Link, error)
}

type VisitRepository interface {
	Record(ctx context.Context, visit domain.Visit) error
}

type LinkCache interface {
	Get(ctx context.Context, code string) (domain.Link, bool, error)
	Set(ctx context.Context, link domain.Link) error
}

type Service struct {
	repository   LinkRepository
	visits       VisitRepository
	cache        LinkCache
	generate     func() (string, error)
	writeTimeout time.Duration
	log          *slog.Logger
}

func New(repository LinkRepository, visits VisitRepository, cache LinkCache, generate func() (string, error), writeTimeout time.Duration, log *slog.Logger) *Service {
	return &Service{
		repository:   repository,
		visits:       visits,
		cache:        cache,
		generate:     generate,
		writeTimeout: writeTimeout,
		log:          log,
	}
}

func (s *Service) Create(ctx context.Context, originalURL string, alias *string) (domain.Link, error) {
	if err := ctx.Err(); err != nil {
		return domain.Link{}, err
	}
	if err := ValidateURL(originalURL); err != nil {
		return domain.Link{}, err
	}
	if alias != nil {
		if err := ValidateAlias(*alias); err != nil {
			return domain.Link{}, err
		}
		return s.repository.Create(ctx, *alias, originalURL)
	}
	for range 5 {
		if err := ctx.Err(); err != nil {
			return domain.Link{}, err
		}
		code, err := s.generate()
		if err != nil {
			return domain.Link{}, fmt.Errorf("generate short code: %w", err)
		}
		link, err := s.repository.Create(ctx, code, originalURL)
		if !errors.Is(err, domain.ErrCodeConflict) {
			return link, err
		}
	}
	return domain.Link{}, ErrCodeGenerationExhausted
}
