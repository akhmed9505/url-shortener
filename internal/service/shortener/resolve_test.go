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
	"unicode/utf8"

	"github.com/akhmed9505/url-shortener/internal/domain"
)

type visitFunc func(context.Context, domain.Visit) error

func (f visitFunc) Record(ctx context.Context, visit domain.Visit) error { return f(ctx, visit) }

type lookupFunc func(context.Context, string) (domain.Link, error)

func (f lookupFunc) FindByCode(ctx context.Context, code string) (domain.Link, error) {
	return f(ctx, code)
}

func (f lookupFunc) Create(context.Context, string, string) (domain.Link, error) {
	panic("unexpected Create during lookup")
}

func TestResolveRecordsRequestTimeAndUserAgent(t *testing.T) {
	want := domain.Link{ID: 42, Code: "docs", OriginalURL: "https://example.org"}
	requestTime := time.Date(2026, 11, 16, 12, 34, 56, 123456000, time.FixedZone("MSK", 3*60*60))
	type contextKey struct{}
	ctx := context.WithValue(t.Context(), contextKey{}, "request")
	var writeContext context.Context
	calls := 0
	repository := lookupFunc(func(got context.Context, code string) (domain.Link, error) {
		if got != ctx || code != want.Code {
			t.Fatal("unexpected lookup arguments")
		}
		return want, nil
	})
	visits := visitFunc(func(child context.Context, visit domain.Visit) error {
		calls++
		writeContext = child
		if child == ctx || child.Value(contextKey{}) != "request" {
			t.Fatal("write must use a child context")
		}
		deadline, ok := child.Deadline()
		if !ok || time.Until(deadline) > 150*time.Millisecond {
			t.Fatal("missing write deadline")
		}
		if visit.LinkID != want.ID || !visit.VisitedAt.Equal(requestTime) || visit.VisitedAt.Location() != time.UTC || visit.UserAgent != "Browser/1.0" {
			t.Fatalf("unexpected visit: %+v", visit)
		}
		return nil
	})
	service := New(repository, visits, missingCache{}, GenerateCode, 150*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	got, err := service.Resolve(ctx, want.Code, "Browser/1.0", requestTime)
	if err != nil || got != want || calls != 1 {
		t.Fatalf("link=%+v calls=%d err=%v", got, calls, err)
	}
	if !errors.Is(writeContext.Err(), context.Canceled) || ctx.Err() != nil {
		t.Fatal("write context must be released independently")
	}
}

func TestResolveLookupFailureDoesNotRecordVisit(t *testing.T) {
	for _, want := range []error{domain.ErrLinkNotFound, domain.ErrStorageUnavailable, context.DeadlineExceeded, errors.New("unexpected SQL error")} {
		t.Run(want.Error(), func(t *testing.T) {
			service := New(lookupFunc(func(context.Context, string) (domain.Link, error) {
				return domain.Link{}, want
			}), visitFunc(func(context.Context, domain.Visit) error {
				t.Fatal("visit recorded after failed lookup")
				return nil
			}), missingCache{}, GenerateCode, 150*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			if _, err := service.Resolve(t.Context(), "docs", "", time.Now()); !errors.Is(err, want) {
				t.Fatalf("error=%v want=%v", err, want)
			}
		})
	}
}

func TestResolveRejectsInvalidCodeAndCanceledContext(t *testing.T) {
	service := New(lookupFunc(func(context.Context, string) (domain.Link, error) {
		t.Fatal("lookup called for invalid request")
		return domain.Link{}, nil
	}), visitFunc(func(context.Context, domain.Visit) error {
		t.Fatal("unexpected visit")
		return nil
	}), missingCache{}, GenerateCode, 150*time.Millisecond, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	for _, code := range []string{"ab", "bad/code", "код"} {
		var validation *ValidationError
		if _, err := service.Resolve(t.Context(), code, "", time.Now()); !errors.As(err, &validation) || validation.Field != "code" {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Resolve(ctx, "docs", "", time.Now()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestResolveWriteFailureIsBestEffort(t *testing.T) {
	var logs bytes.Buffer
	want := domain.Link{ID: 1, Code: "docs", OriginalURL: "https://example.org/?token=private-secret"}
	service := New(lookupFunc(func(context.Context, string) (domain.Link, error) { return want, nil }),
		visitFunc(func(context.Context, domain.Visit) error { return errors.New("SQL private-secret") }),
		missingCache{}, GenerateCode, 150*time.Millisecond, slog.New(slog.NewJSONHandler(&logs, nil)))
	got, err := service.Resolve(t.Context(), want.Code, "private-secret", time.Now())
	if err != nil || got != want {
		t.Fatalf("link=%+v err=%v", got, err)
	}
	if !strings.Contains(logs.String(), "visit_write_failed") || strings.Contains(logs.String(), "private-secret") || strings.Contains(logs.String(), "SQL") {
		t.Fatalf("unexpected log: %s", &logs)
	}
}

func TestResolveWriteTimeoutPreservesLinkAndParentContext(t *testing.T) {
	var logs bytes.Buffer
	want := domain.Link{ID: 1, Code: "docs", OriginalURL: "https://example.org"}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	service := New(lookupFunc(func(context.Context, string) (domain.Link, error) { return want, nil }),
		visitFunc(func(child context.Context, _ domain.Visit) error {
			<-child.Done()
			return child.Err()
		}), missingCache{}, GenerateCode, 5*time.Millisecond, slog.New(slog.NewJSONHandler(&logs, nil)))
	got, err := service.Resolve(ctx, "docs", "", time.Now())
	if err != nil || got != want || ctx.Err() != nil {
		t.Fatalf("link=%+v err=%v parent=%v", got, err, ctx.Err())
	}
	if !strings.Contains(logs.String(), `"reason":"timeout"`) {
		t.Fatalf("unexpected log: %s", &logs)
	}
}

func TestResolveWriteHonorsParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	parentDeadline, _ := ctx.Deadline()
	service := New(lookupFunc(func(context.Context, string) (domain.Link, error) {
		return domain.Link{ID: 1, Code: "docs"}, nil
	}), visitFunc(func(child context.Context, _ domain.Visit) error {
		deadline, _ := child.Deadline()
		if !deadline.Equal(parentDeadline) {
			t.Fatal("write escaped parent deadline")
		}
		return nil
	}), missingCache{}, GenerateCode, time.Second, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if _, err := service.Resolve(ctx, "docs", "", time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestNormalizeUserAgent(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"empty", "", ""},
		{"unchanged", "Browser/1.0", "Browser/1.0"},
		{"ASCII limit", strings.Repeat("a", 1025), strings.Repeat("a", 1024)},
		{"multibyte", strings.Repeat("я", 513), strings.Repeat("я", 512)},
		{"split rune", strings.Repeat("a", 1023) + "я", strings.Repeat("a", 1023)},
		{"emoji", strings.Repeat("😀", 300), strings.Repeat("😀", 256)},
		{"invalid UTF-8", "a\xff\xfeb", "a\uFFFDb"},
		{"NUL", "a\x00b", "a\uFFFDb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeUserAgent(tc.raw)
			if got != tc.want || !utf8.ValidString(got) || len(got) > 1024 {
				t.Fatalf("invalid normalized User-Agent: %q", got)
			}
		})
	}
}

// These tests exercise PostgreSQL lookup and best-effort visit recording.
type missingCache struct{}

func (missingCache) Get(context.Context, string) (domain.Link, bool, error) {
	return domain.Link{}, false, nil
}
func (missingCache) Set(context.Context, domain.Link) error { return nil }
