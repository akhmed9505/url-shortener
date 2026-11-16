package shortener

import (
	"context"
	"errors"
	"testing"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

type repositoryFunc func(context.Context, string, string) (domain.Link, error)

func (f repositoryFunc) Create(ctx context.Context, code, originalURL string) (domain.Link, error) {
	return f(ctx, code, originalURL)
}

func TestCreateWithAlias(t *testing.T) {
	alias := "My_alias"
	want := domain.Link{ID: 1, Code: alias, OriginalURL: "https://example.org"}
	repository := repositoryFunc(func(ctx context.Context, code, raw string) (domain.Link, error) {
		if ctx != t.Context() || code != want.Code || raw != want.OriginalURL {
			t.Fatal("repository received unexpected arguments")
		}
		return want, nil
	})
	service := New(repository, func() (string, error) {
		t.Fatal("generator called for explicit alias")
		return "", nil
	})
	got, err := service.Create(t.Context(), want.OriginalURL, &alias)
	if err != nil || got != want {
		t.Fatalf("link=%+v err=%v", got, err)
	}
}

func TestCreateRetriesRandomCollisions(t *testing.T) {
	calls, generated := 0, 0
	codes := []string{"Conflict", "Success8"}
	service := New(repositoryFunc(func(_ context.Context, code, raw string) (domain.Link, error) {
		calls++
		if code == codes[0] {
			return domain.Link{}, domain.ErrCodeConflict
		}
		return domain.Link{ID: 2, Code: code, OriginalURL: raw}, nil
	}), func() (string, error) {
		code := codes[generated]
		generated++
		return code, nil
	})
	got, err := service.Create(t.Context(), "https://example.org", nil)
	if err != nil || got.Code != codes[1] || calls != 2 || generated != 2 {
		t.Fatalf("link=%+v calls=%d generated=%d err=%v", got, calls, generated, err)
	}
}

func TestCreateStopsAfterFiveCollisions(t *testing.T) {
	calls, generated := 0, 0
	service := New(repositoryFunc(func(context.Context, string, string) (domain.Link, error) {
		calls++
		return domain.Link{}, domain.ErrCodeConflict
	}), func() (string, error) {
		generated++
		return "Conflict", nil
	})
	_, err := service.Create(t.Context(), "https://example.org", nil)
	if !errors.Is(err, ErrCodeGenerationExhausted) || calls != 5 || generated != 5 {
		t.Fatalf("calls=%d generated=%d err=%v", calls, generated, err)
	}
}

func TestCreateDoesNotRetryOtherErrorsOrAliasConflict(t *testing.T) {
	alias := "explicit"
	for _, tc := range []struct {
		name  string
		alias *string
		err   error
	}{
		{"alias conflict", &alias, domain.ErrCodeConflict},
		{"storage unavailable", nil, domain.ErrStorageUnavailable},
		{"other database failure", nil, errors.New("database failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			service := New(repositoryFunc(func(context.Context, string, string) (domain.Link, error) {
				calls++
				return domain.Link{}, tc.err
			}), func() (string, error) { return "Random12", nil })
			_, err := service.Create(t.Context(), "https://example.org", tc.alias)
			if !errors.Is(err, tc.err) || calls != 1 {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestCreateGeneratorFailure(t *testing.T) {
	want := errors.New("entropy failure")
	service := New(repositoryFunc(func(context.Context, string, string) (domain.Link, error) {
		t.Fatal("repository called after generator failure")
		return domain.Link{}, nil
	}), func() (string, error) { return "", want })
	if _, err := service.Create(t.Context(), "https://example.org", nil); !errors.Is(err, want) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateRejectsInvalidInputBeforeRepository(t *testing.T) {
	empty := ""
	service := New(repositoryFunc(func(context.Context, string, string) (domain.Link, error) {
		t.Fatal("repository called for invalid input")
		return domain.Link{}, nil
	}), func() (string, error) {
		t.Fatal("generator called for invalid input")
		return "", nil
	})
	for _, tc := range []struct {
		url   string
		alias *string
	}{
		{"bad", nil}, {"https://example.org", &empty},
	} {
		var validation *ValidationError
		if _, err := service.Create(t.Context(), tc.url, tc.alias); !errors.As(err, &validation) {
			t.Fatalf("expected validation error, got %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Create(ctx, "https://example.org", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
