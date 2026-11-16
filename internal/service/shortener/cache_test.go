package shortener

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

type cacheStub struct {
	get func(context.Context, string) (domain.Link, bool, error)
	set func(context.Context, domain.Link) error
}

func (c cacheStub) Get(ctx context.Context, code string) (domain.Link, bool, error) {
	return c.get(ctx, code)
}
func (c cacheStub) Set(ctx context.Context, link domain.Link) error { return c.set(ctx, link) }

func TestResolveCacheHitSkipsLookupAndSurvivesVisitFailure(t *testing.T) {
	want := domain.Link{ID: 1, Code: "cached", OriginalURL: "https://example.org"}
	visits := 0
	cache := cacheStub{
		get: func(context.Context, string) (domain.Link, bool, error) { return want, true, nil },
		set: func(context.Context, domain.Link) error { t.Fatal("hit must not refresh TTL"); return nil },
	}
	service := New(lookupFunc(func(context.Context, string) (domain.Link, error) {
		t.Fatal("cache hit performed PostgreSQL lookup")
		return domain.Link{}, nil
	}), visitFunc(func(_ context.Context, event domain.Visit) error {
		visits++
		if event.LinkID != want.ID {
			t.Fatal("wrong link ID")
		}
		return domain.ErrStorageUnavailable
	}), cache, GenerateCode, 150*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	got, err := service.Resolve(t.Context(), want.Code, "Browser", time.Now())
	if err != nil || got != want || visits != 1 {
		t.Fatalf("link=%+v visits=%d err=%v", got, visits, err)
	}
}

func TestResolveCacheMissAndInvalidValuesFallBack(t *testing.T) {
	want := domain.Link{ID: 1, Code: "cached", OriginalURL: "https://example.org"}
	for _, tc := range []struct {
		name  string
		value domain.Link
		hit   bool
		err   error
	}{
		{name: "miss"},
		{name: "read failure", err: errors.New("private-secret")},
		{name: "read timeout", err: context.DeadlineExceeded},
		{name: "invalid ID", hit: true, value: domain.Link{Code: want.Code, OriginalURL: want.OriginalURL}},
		{name: "unsafe URL", hit: true, value: domain.Link{ID: 1, Code: want.Code, OriginalURL: "javascript:private-secret"}},
		{name: "wrong code", hit: true, value: domain.Link{ID: 1, Code: "other", OriginalURL: want.OriginalURL}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			reads, fills, visits := 0, 0, 0
			cache := cacheStub{
				get: func(context.Context, string) (domain.Link, bool, error) { return tc.value, tc.hit, tc.err },
				set: func(_ context.Context, link domain.Link) error {
					fills++
					if link != want {
						t.Fatal("wrong cached value")
					}
					return nil
				},
			}
			service := New(lookupFunc(func(context.Context, string) (domain.Link, error) { reads++; return want, nil }), visitFunc(func(context.Context, domain.Visit) error { visits++; return nil }), cache, GenerateCode, 150*time.Millisecond, slog.New(slog.NewJSONHandler(&logs, nil)))
			got, err := service.Resolve(t.Context(), want.Code, "", time.Now())
			if err != nil || got != want || reads != 1 || fills != 1 || visits != 1 {
				t.Fatalf("link=%+v reads=%d fills=%d visits=%d err=%v", got, reads, fills, visits, err)
			}
			if strings.Contains(logs.String(), "private-secret") {
				t.Fatal("cache log exposed raw error or payload")
			}
		})
	}
}

func TestResolveCacheFillIsBestEffort(t *testing.T) {
	want := domain.Link{ID: 1, Code: "cached", OriginalURL: "https://example.org"}
	cache := cacheStub{
		get: func(context.Context, string) (domain.Link, bool, error) { return domain.Link{}, false, nil },
		set: func(context.Context, domain.Link) error { return errors.New("private-secret") },
	}
	visits := 0
	var logs bytes.Buffer
	service := New(lookupFunc(func(context.Context, string) (domain.Link, error) { return want, nil }), visitFunc(func(context.Context, domain.Visit) error { visits++; return nil }), cache, GenerateCode, 150*time.Millisecond, slog.New(slog.NewJSONHandler(&logs, nil)))
	got, err := service.Resolve(t.Context(), want.Code, "", time.Now())
	if err != nil || got != want || visits != 1 {
		t.Fatalf("link=%+v visits=%d err=%v", got, visits, err)
	}
	if !strings.Contains(logs.String(), "cache_error") || strings.Contains(logs.String(), "private-secret") {
		t.Fatalf("unsafe log: %s", &logs)
	}
}

func TestResolveDoesNotCacheMissingLinks(t *testing.T) {
	cache := cacheStub{
		get: func(context.Context, string) (domain.Link, bool, error) { return domain.Link{}, false, nil },
		set: func(context.Context, domain.Link) error { t.Fatal("negative cache entry created"); return nil },
	}
	service := New(lookupFunc(func(context.Context, string) (domain.Link, error) { return domain.Link{}, domain.ErrLinkNotFound }), visitFunc(func(context.Context, domain.Visit) error { t.Fatal("unexpected visit"); return nil }), cache, GenerateCode, 150*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if _, err := service.Resolve(t.Context(), "missing", "", time.Now()); !errors.Is(err, domain.ErrLinkNotFound) {
		t.Fatalf("unexpected error: %v", err)
	}
}
