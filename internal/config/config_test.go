package config

import (
	"github.com/jackc/pgx/v5"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func clearEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HTTP_ADDR", "LOG_LEVEL", "HTTP_READ_HEADER_TIMEOUT", "PUBLIC_BASE_URL", "ANALYTICS_WRITE_TIMEOUT",
		"HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT",
		"PGHOST", "PGPORT", "PGUSER", "PGPASSWORD", "PGDATABASE", "PGSSLMODE", "POSTGRES_DSN", "POSTGRES_MAX_CONNS", "POSTGRES_MIN_CONNS", "POSTGRES_CONNECT_TIMEOUT",
		"REDIS_ADDR", "REDIS_USERNAME", "REDIS_PASSWORD", "REDIS_DB", "REDIS_CACHE_TTL",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{"PGHOST": "localhost", "PGPORT": "5432", "PGUSER": "shortener", "PGPASSWORD": "shortener", "PGDATABASE": "shortener", "PGSSLMODE": "disable"} {
		t.Setenv(key, value)
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnvironment(t)
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{HTTP: HTTP{
		Addr:              ":8080",
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}, LogLevel: slog.LevelInfo, Postgres: Postgres{
		DSN:      "postgres://shortener:shortener@localhost:5432/shortener?sslmode=disable",
		MaxConns: 10, MinConns: 0, ConnectTimeout: 5 * time.Second,
	}}
	want.PublicBaseURL = "http://localhost:8080"
	want.AnalyticsWriteTimeout = 150 * time.Millisecond
	want.Redis = Redis{Addr: "localhost:6379", CacheTTL: time.Hour}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	clearEnvironment(t)
	for key, value := range map[string]string{
		"ANALYTICS_WRITE_TIMEOUT":  "25ms",
		"PUBLIC_BASE_URL":          "https://sho.rt/",
		"HTTP_ADDR":                "[::1]:9000",
		"LOG_LEVEL":                "debug",
		"HTTP_READ_HEADER_TIMEOUT": "250ms",
		"HTTP_READ_TIMEOUT":        "2s",
		"HTTP_WRITE_TIMEOUT":       "3s",
		"HTTP_IDLE_TIMEOUT":        "1m30s",
		"PGUSER":                   "test", "PGPASSWORD": "test", "PGDATABASE": "test", "PGPORT": "5433",
		"POSTGRES_MAX_CONNS":       "20",
		"POSTGRES_MIN_CONNS":       "2",
		"POSTGRES_CONNECT_TIMEOUT": "2s",
	} {
		t.Setenv(key, value)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	want := Config{HTTP: HTTP{
		Addr:              "[::1]:9000",
		ReadHeaderTimeout: 250 * time.Millisecond,
		ReadTimeout:       2 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       90 * time.Second,
	}, LogLevel: slog.LevelDebug, Postgres: Postgres{
		DSN:      "postgres://test:test@localhost:5433/test?sslmode=disable",
		MaxConns: 20, MinConns: 2, ConnectTimeout: 2 * time.Second,
	}}
	want.PublicBaseURL = "https://sho.rt"
	want.AnalyticsWriteTimeout = 25 * time.Millisecond
	want.Redis = Redis{Addr: "localhost:6379", CacheTTL: time.Hour}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadRejectsInvalidAddress(t *testing.T) {
	for _, address := range []string{"", "8080", ":", ":http", ":0", ":-1", ":65536", "::1:8080"} {
		t.Run(address, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("HTTP_ADDR", address)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "HTTP_ADDR") {
				t.Fatalf("Load() error = %v, want HTTP_ADDR validation error", err)
			}
		})
	}
}

func TestLoadRejectsInvalidLogLevel(t *testing.T) {
	for _, level := range []string{"", "verbose"} {
		t.Run(level, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("LOG_LEVEL", level)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") {
				t.Fatalf("Load() error = %v, want LOG_LEVEL validation error", err)
			}
		})
	}
}

func TestLoadRejectsInvalidTimeouts(t *testing.T) {
	for _, key := range []string{"HTTP_READ_HEADER_TIMEOUT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_IDLE_TIMEOUT", "POSTGRES_CONNECT_TIMEOUT", "ANALYTICS_WRITE_TIMEOUT"} {
		for _, value := range []string{"", "0s", "-1s", "10", "invalid", "999999999999999999999h"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				clearEnvironment(t)
				t.Setenv(key, value)
				_, err := Load()
				if err == nil || !strings.Contains(err.Error(), key) {
					t.Fatalf("Load() error = %v, want %s validation error", err, key)
				}
			})
		}
	}
}

func TestLoadRejectsHeaderTimeoutExceedingReadTimeout(t *testing.T) {
	clearEnvironment(t)
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "11s")
	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted a header timeout exceeding the read timeout")
	}
}

func TestLoadRejectsInvalidPostgresConfiguration(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{"PGPASSWORD", ""},
		{"PGPORT", "invalid"},
		{"POSTGRES_MAX_CONNS", "0"},
		{"POSTGRES_MAX_CONNS", "-1"},
		{"POSTGRES_MAX_CONNS", "2147483648"},
		{"POSTGRES_MIN_CONNS", "-1"},
		{"POSTGRES_MIN_CONNS", "11"},
		{"POSTGRES_MIN_CONNS", "invalid"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatal("validation error exposes DSN credentials")
			}
		})
	}
}

func TestLoadRejectsInvalidPublicBaseURL(t *testing.T) {
	for _, value := range []string{
		"", "example.org", "ftp://example.org", "https:///path", "https://:443",
		"https://user:secret@example.org", "https://example.org/path", "https://example.org?x=1",
		"https://example.org?", "https://example.org#", "https://example.org\\evil",
		"https://example.org:0", "https://example.org:65536", "https://example.org:",
		"http://[bad]", "https://example.org/%2f", "https://example.org/%2F", "https://example.org/%2e", "https://example.org/%5c", "https://example.org/%252f", "https://example.org/\u00a0", "https://example.org/\x7f",
		"http://[bad]/",
	} {
		t.Run(value, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv("PUBLIC_BASE_URL", value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadRejectsAnalyticsTimeoutExceedingRequestBudget(t *testing.T) {
	for _, value := range []string{"2s", "3s"} {
		clearEnvironment(t)
		t.Setenv("ANALYTICS_WRITE_TIMEOUT", value)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ANALYTICS_WRITE_TIMEOUT") {
			t.Fatalf("expected timeout validation error, got %v", err)
		}
	}
}

func TestLoadRequiresPostgresEnvironment(t *testing.T) {
	for _, key := range []string{"PGHOST", "PGPORT", "PGUSER", "PGPASSWORD", "PGDATABASE", "PGSSLMODE"} {
		t.Run(key, func(t *testing.T) {
			clearEnvironment(t)
			t.Setenv(key, "")
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected missing %s error", key)
			}
		})
	}
}

func TestPostgresCredentialsRoundTrip(t *testing.T) {
	clearEnvironment(t)
	password := "test@:# /'value"
	t.Setenv("PGPASSWORD", password)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := pgx.ParseConfig(cfg.Postgres.DSN)
	if err != nil || connection.Password != password {
		t.Fatal("PostgreSQL credential encoding failed")
	}
	t.Setenv("POSTGRES_DSN", "postgres://legacy")
	if _, err := Load(); err == nil {
		t.Fatal("conflicting legacy DSN accepted")
	}
}
