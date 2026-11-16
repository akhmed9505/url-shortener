package shortener

import (
	"context"
	"errors"
	"fmt"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

var ErrCodeGenerationExhausted = errors.New("short code generation attempts exhausted")

type LinkRepository interface {
	Create(ctx context.Context, code, originalURL string) (domain.Link, error)
}

type Service struct {
	repository LinkRepository
	generate   func() (string, error)
}

func New(repository LinkRepository, generate func() (string, error)) *Service {
	return &Service{repository: repository, generate: generate}
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
