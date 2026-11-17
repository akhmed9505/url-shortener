package shortener

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

// ErrCodeGenerationExhausted reports that all random code generation attempts collided.
var ErrCodeGenerationExhausted = errors.New("short code generation attempts exhausted")

// Service implements link creation and resolution.
type Service struct {
	repository   LinkRepository
	visits       VisitRepository
	cache        LinkCache
	generate     func() (string, error)
	writeTimeout time.Duration
	log          *slog.Logger
}

// New creates a link service with explicit storage, cache and generator dependencies.
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

// Create stores a link with a custom alias or generated code.
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
