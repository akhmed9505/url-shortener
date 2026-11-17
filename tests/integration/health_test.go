//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/config"
	httpdelivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	httphandler "github.com/akhmed9505/url-shortener/internal/delivery/http/handler"
	"github.com/akhmed9505/url-shortener/internal/delivery/http/middleware"
	httpresponse "github.com/akhmed9505/url-shortener/internal/delivery/http/response"
	pginfra "github.com/akhmed9505/url-shortener/internal/infra/postgres"
	"github.com/akhmed9505/url-shortener/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReadinessPostgresAvailabilityAndRecovery(t *testing.T) {
	pool, _ := newDatabase(t, true)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	server := httptest.NewServer(httpdelivery.NewRouter(httphandler.NewHandler(nil, nil, func(ctx context.Context) error { return pginfra.CheckReadiness(ctx, pool) }, "https://sho.rt", log), log, middleware.NewRateLimits(time.Now)))
	defer server.Close()
	checkHealthStatus(t, server.URL, "/health/ready", 200)
	admin, err := pgx.Connect(t.Context(), os.Getenv("TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	database := pool.Config().ConnConfig.Database
	identifier := pgx.Identifier{database}.Sanitize()
	if _, err := admin.Exec(t.Context(), "ALTER DATABASE "+identifier+" ALLOW_CONNECTIONS false"); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(context.Background(), "ALTER DATABASE "+identifier+" ALLOW_CONNECTIONS true")
	if _, err := admin.Exec(t.Context(), "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1", database); err != nil {
		t.Fatal(err)
	}
	pool.Reset()
	checkHealthStatus(t, server.URL, "/health/ready", 503)
	checkHealthStatus(t, server.URL, "/health/live", 200)
	if _, err := admin.Exec(t.Context(), "ALTER DATABASE "+identifier+" ALLOW_CONNECTIONS true"); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 200)
}

func TestReadinessPoolWaitIsBounded(t *testing.T) {
	pool, _ := newDatabase(t, true)
	cfg := pool.Config()
	cfg.MaxConns = 1
	limited, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Close()
	conn, err := limited.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	server := httptest.NewServer(httpdelivery.NewRouter(httphandler.NewHandler(nil, nil, func(ctx context.Context) error { return pginfra.CheckReadiness(ctx, limited) }, "https://sho.rt", log), log, middleware.NewRateLimits(time.Now)))
	defer server.Close()
	started := time.Now()
	checkHealthStatus(t, server.URL, "/health/ready", 503)
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("readiness elapsed=%v", elapsed)
	}
	checkHealthStatus(t, server.URL, "/health/live", 200)
	conn.Release()
	checkHealthStatus(t, server.URL, "/health/ready", 200)
}

func checkHealthStatus(t *testing.T, base, path string, status int) {
	t.Helper()
	response, err := (&http.Client{Timeout: 3 * time.Second}).Get(base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != status {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("%s status=%d body=%s", path, response.StatusCode, body)
	}
	if status == 503 {
		var body httpresponse.ErrorResponse
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil || body.Error.Code != "not_ready" {
			t.Fatalf("body=%+v err=%v", body, err)
		}
	}
}

func TestApplicationReadinessRequiresCurrentMigrations(t *testing.T) {
	pool, provider := newDatabase(t, false)
	redisClient := realRedis(t)
	state := startApplication(t, pool, config.Redis{Addr: redisClient.Options().Addr, CacheTTL: time.Hour})
	checkHealthStatus(t, state.url, "/health/ready", 503)
	var historyExists bool
	if err := pool.QueryRow(t.Context(), "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&historyExists); err != nil || historyExists {
		t.Fatalf("probe changed migration history: exists=%v err=%v", historyExists, err)
	}
	checkHealthStatus(t, state.url, "/health/live", 200)
	var versionTableExists bool
	if err := pool.QueryRow(t.Context(), "SELECT to_regclass('goose_db_version') IS NOT NULL").Scan(&versionTableExists); err != nil || versionTableExists {
		t.Fatalf("probe changed migration metadata: exists=%v err=%v", versionTableExists, err)
	}
	if _, err := provider.UpTo(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, state.url, "/health/ready", 503)
	checkHealthStatus(t, state.url, "/health/live", 200)
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, state.url, "/health/ready", 200)
	if _, err := provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, state.url, "/health/ready", 503)
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, state.url, "/health/ready", 200)
}

func TestReadinessRejectsMigrationHistoryMismatch(t *testing.T) {
	pool, _ := newDatabase(t, true)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	check := func(ctx context.Context) error { return pginfra.CheckReadiness(ctx, pool) }
	server := httptest.NewServer(httpdelivery.NewRouter(httphandler.NewHandler(nil, nil, check, "https://sho.rt", log), log, middleware.NewRateLimits(time.Now)))
	defer server.Close()
	versions, err := migrations.Versions()
	if err != nil {
		t.Fatal(err)
	}
	latest := versions[len(versions)-1]
	if _, err := pool.Exec(t.Context(), "INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,true)", latest+1); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 503)
	if _, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=$1", latest+1); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 200)
	// Only checking MAX(version_id) would miss this gap.
	if _, err := pool.Exec(t.Context(), "DELETE FROM goose_db_version WHERE version_id=$1", versions[0]); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 503)
	if _, err := pool.Exec(t.Context(), "INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,true)", versions[0]); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 200)
	// The latest record for a version takes precedence in legacy Goose histories.
	if _, err := pool.Exec(t.Context(), "INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,false)", latest); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 503)
	if _, err := pool.Exec(t.Context(), "INSERT INTO goose_db_version(version_id,is_applied) VALUES($1,true)", latest); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 200)
	if _, err := pool.Exec(t.Context(), "DROP TABLE goose_db_version"); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 503)
	checkHealthStatus(t, server.URL, "/health/live", 200)
}

func TestReadinessSchemaQueryDeadline(t *testing.T) {
	pool, _ := newDatabase(t, true)
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(t.Context(), "LOCK TABLE goose_db_version IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	check := func(ctx context.Context) error { return pginfra.CheckReadiness(ctx, pool) }
	server := httptest.NewServer(httpdelivery.NewRouter(httphandler.NewHandler(nil, nil, check, "https://sho.rt", log), log, middleware.NewRateLimits(time.Now)))
	defer server.Close()
	started := time.Now()
	checkHealthStatus(t, server.URL, "/health/ready", 503)
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("schema check elapsed=%v", elapsed)
	}
	checkHealthStatus(t, server.URL, "/health/live", 200)
	if err := blocker.Rollback(t.Context()); err != nil {
		t.Fatal(err)
	}
	checkHealthStatus(t, server.URL, "/health/ready", 200)
}

func TestReadinessRejectsMissingApplicationTables(t *testing.T) {
	for _, table := range []string{"links", "visits"} {
		t.Run(table, func(t *testing.T) {
			pool, _ := newDatabase(t, true)
			log := slog.New(slog.NewJSONHandler(io.Discard, nil))
			check := func(ctx context.Context) error { return pginfra.CheckReadiness(ctx, pool) }
			server := httptest.NewServer(httpdelivery.NewRouter(httphandler.NewHandler(nil, nil, check, "https://sho.rt", log), log, middleware.NewRateLimits(time.Now)))
			defer server.Close()
			checkHealthStatus(t, server.URL, "/health/ready", 200)
			if _, err := pool.Exec(t.Context(), "DROP TABLE "+pgx.Identifier{table}.Sanitize()+" CASCADE"); err != nil {
				t.Fatal(err)
			}
			checkHealthStatus(t, server.URL, "/health/ready", 503)
			checkHealthStatus(t, server.URL, "/health/live", 200)
		})
	}
}
