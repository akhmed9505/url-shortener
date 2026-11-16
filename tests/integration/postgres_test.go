//go:build integration

package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/config"
	"github.com/akhmed9505/url-shortener/internal/domain"
	pginfra "github.com/akhmed9505/url-shortener/internal/infra/postgres"
	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Every test owns a new database, including its migration version table.
func newDatabase(t *testing.T, migrate bool) (*pgxpool.Pool, *goose.Provider) {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("TEST_POSTGRES_DSN must point to a dedicated test PostgreSQL instance with CREATEDB permission")
	}
	databaseURL, err := url.Parse(dsn)
	if err != nil || (databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql") {
		t.Fatal("TEST_POSTGRES_DSN must be a PostgreSQL URL")
	}
	ctx := t.Context()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = admin.Close(context.Background()) })
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "shortener_test_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+identifier+" TEMPLATE template0 ENCODING 'UTF8'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanupCtx, "DROP DATABASE "+identifier+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database: %v", err)
		}
	})
	databaseURL.Path = "/" + name
	databaseURL.RawPath = ""
	connConfig, err := pgx.ParseConfig(databaseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pginfra.NewPool(ctx, config.Postgres{
		DSN: databaseURL.String(), MaxConns: 20, MinConns: 0, ConnectTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var currentDatabase string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&currentDatabase); err != nil || currentDatabase != name {
		t.Fatalf("test database isolation failed: database=%q err=%v", currentDatabase, err)
	}
	db := stdlib.OpenDB(*connConfig)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if migrate {
		if _, err := provider.Up(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return pool, provider
}

func TestCreateLink(t *testing.T) {
	pool, _ := newDatabase(t, true)
	repo := pgrepo.NewLinkRepository(pool)
	before := time.Now().UTC().Add(-time.Second)
	link, err := repo.Create(t.Context(), "Abc_123", "https://example.org/path?q='value'")
	if err != nil {
		t.Fatal(err)
	}
	if link.ID <= 0 || link.Code != "Abc_123" || link.OriginalURL != "https://example.org/path?q='value'" {
		t.Fatalf("unexpected link: %+v", link)
	}
	if link.CreatedAt.Location() != time.UTC || link.CreatedAt.Before(before) || link.CreatedAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("unexpected creation time: %v", link.CreatedAt)
	}
	var timezone string
	if err := pool.QueryRow(t.Context(), "SHOW timezone").Scan(&timezone); err != nil || timezone != "UTC" {
		t.Fatalf("timezone=%q, err=%v", timezone, err)
	}
}

func TestFindLink(t *testing.T) {
	pool, _ := newDatabase(t, true)
	repo := pgrepo.NewLinkRepository(pool)
	want, err := repo.Create(t.Context(), "Find123", "https://example.org")
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.FindByCode(t.Context(), want.Code)
	if err != nil || got != want {
		t.Fatalf("FindByCode() = %+v, %v; want %+v", got, err, want)
	}
	for _, code := range []string{"missing", "find123", "' OR true --"} {
		if _, err := repo.FindByCode(t.Context(), code); !errors.Is(err, domain.ErrLinkNotFound) {
			t.Fatalf("FindByCode(%q) error = %v", code, err)
		}
	}
}

func TestCodeConflict(t *testing.T) {
	pool, _ := newDatabase(t, true)
	repo := pgrepo.NewLinkRepository(pool)
	want, err := repo.Create(t.Context(), "same", "https://example.org/first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(t.Context(), "same", "https://example.org/second"); !errors.Is(err, domain.ErrCodeConflict) {
		t.Fatalf("expected code conflict, got %v", err)
	}
	got, err := repo.FindByCode(t.Context(), "same")
	if err != nil || got != want {
		t.Fatalf("original link changed: %+v, %v", got, err)
	}
}

func TestConcurrentCodeCreation(t *testing.T) {
	pool, _ := newDatabase(t, true)
	repo := pgrepo.NewLinkRepository(pool)
	const workers = 16
	start := make(chan struct{})
	results := make(chan error, workers)
	for range workers {
		go func() {
			<-start
			_, err := repo.Create(t.Context(), "shared", "https://example.org")
			results <- err
		}()
	}
	close(start)
	successes, conflicts := 0, 0
	for range workers {
		err := <-results
		switch {
		case err == nil:
			successes++
		case errors.Is(err, domain.ErrCodeConflict):
			conflicts++
		default:
			t.Errorf("unexpected create error: %v", err)
		}
	}
	if successes != 1 || conflicts != workers-1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM links").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestDatabaseConstraints(t *testing.T) {
	pool, _ := newDatabase(t, true)
	for _, tc := range []struct {
		name      string
		code, url any
		state     string
	}{
		{"null code", nil, "https://example.org", "23502"},
		{"null URL", "abc", nil, "23502"},
		{"empty code", "", "https://example.org", "23514"},
		{"short code", "ab", "https://example.org", "23514"},
		{"long code", strings.Repeat("a", 33), "https://example.org", "23514"},
		{"invalid code", "ab/c", "https://example.org", "23514"},
		{"unicode code", "код", "https://example.org", "23514"},
		{"empty URL", "abc", "", "23514"},
		{"long URL", "abc", strings.Repeat("a", 2049), "23514"},
		{"URL byte limit", "abc", strings.Repeat("я", 1025), "23514"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), "INSERT INTO links(code, original_url) VALUES ($1, $2)", tc.code, tc.url)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != tc.state {
				t.Fatalf("expected SQLSTATE %s, got %v", tc.state, err)
			}
		})
	}
	for _, code := range []string{"abc", strings.Repeat("A", 32), "a_Z-9"} {
		if _, err := pool.Exec(t.Context(), "INSERT INTO links(code, original_url) VALUES ($1, $2)", code, strings.Repeat("a", 2048)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := pool.Exec(t.Context(), "INSERT INTO links(code, original_url) VALUES ('abc', 'https://example.org')")
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "links_code_key" {
		t.Fatalf("expected links_code_key violation, got %v", err)
	}
}

func TestOtherDatabaseErrorsAreNotCodeConflicts(t *testing.T) {
	pool, _ := newDatabase(t, true)
	repo := pgrepo.NewLinkRepository(pool)
	if _, err := repo.Create(t.Context(), "bad/code", "https://example.org"); err == nil || errors.Is(err, domain.ErrCodeConflict) {
		t.Fatalf("expected check violation, got %v", err)
	}
	if _, err := pool.Exec(t.Context(), "CREATE UNIQUE INDEX test_unique_url ON links(original_url)"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(t.Context(), "first", "https://example.org"); err != nil {
		t.Fatal(err)
	}
	_, err := repo.Create(t.Context(), "other", "https://example.org")
	var pgErr *pgconn.PgError
	if errors.Is(err, domain.ErrCodeConflict) || !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("expected original unrelated unique violation, got %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repo.FindByCode(ctx, "first"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestMigrationUpDown(t *testing.T) {
	pool, provider := newDatabase(t, false)
	for _, up := range []bool{true, false, true} {
		var err error
		var wantVersion int64
		if up {
			_, err = provider.Up(t.Context())
			wantVersion = 1
		} else {
			_, err = provider.Down(t.Context())
		}
		if err != nil {
			t.Fatal(err)
		}
		version, err := provider.GetDBVersion(t.Context())
		if err != nil || version != wantVersion {
			t.Fatalf("version=%d err=%v, want %d", version, err, wantVersion)
		}
		var exists bool
		if err := pool.QueryRow(t.Context(), "SELECT to_regclass('public.links') IS NOT NULL").Scan(&exists); err != nil || exists != up {
			t.Fatalf("links exists=%v err=%v", exists, err)
		}
	}
}
