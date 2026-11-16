//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/config"
	httpdelivery "github.com/akhmed9505/url-shortener/internal/delivery/http"
	"github.com/akhmed9505/url-shortener/internal/domain"
	redisinfra "github.com/akhmed9505/url-shortener/internal/infra/redis"
	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	rediscache "github.com/akhmed9505/url-shortener/internal/repository/redis"
	"github.com/akhmed9505/url-shortener/internal/service/shortener"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func realRedis(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("TEST_REDIS_ADDR")
	if addr == "" {
		t.Fatal("TEST_REDIS_ADDR must point to a dedicated test Redis instance")
	}
	client := redisinfra.NewClient(config.Redis{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), config.RedisOperationTimeout)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Redis fixture unavailable: %v", err)
	}
	return client
}

func cacheFixture(t *testing.T) (*pgxpool.Pool, *redis.Client, domain.Link) {
	t.Helper()
	pool, _ := newDatabase(t, true)
	client := realRedis(t)
	code, err := shortener.GenerateCode()
	if err != nil {
		t.Fatal(err)
	}
	link, err := pgrepo.NewLinkRepository(pool).Create(t.Context(), code, "https://example.org/path?token=private-secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), config.RedisOperationTimeout)
		defer cancel()
		if err := client.Del(ctx, "shortener:link:v1:"+code).Err(); err != nil {
			t.Errorf("remove test cache key: %v", err)
		}
	})
	return pool, client, link
}

func cachedServer(t *testing.T, pool *pgxpool.Pool, cache shortener.LinkCache, logs io.Writer) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewJSONHandler(logs, nil))
	service := shortener.New(pgrepo.NewLinkRepository(pool), pgrepo.NewVisitRepository(pool), cache, shortener.GenerateCode, 150*time.Millisecond, log)
	server := httptest.NewServer(httpdelivery.NewRouter(httpdelivery.NewHandler(service, nil, pool.Ping, "https://sho.rt", log)))
	t.Cleanup(server.Close)
	server.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	server.Client().Timeout = 5 * time.Second
	return server
}

func TestCacheMissFillsFromPostgresAndHitSkipsLookup(t *testing.T) {
	pool, client, link := cacheFixture(t)
	cache := rediscache.NewLinkCache(client, time.Hour)
	if _, hit, err := cache.Get(t.Context(), link.Code); err != nil || hit {
		t.Fatalf("hit=%v err=%v", hit, err)
	}
	server := cachedServer(t, pool, cache, io.Discard)
	status, headers, _ := redirectRequest(t, server, http.MethodGet, link.Code, "Browser")
	if status != 302 || headers.Get("Location") != link.OriginalURL {
		t.Fatalf("status=%d headers=%v", status, headers)
	}
	cached, hit, err := cache.Get(t.Context(), link.Code)
	if err != nil || !hit || cached.ID != link.ID || cached.OriginalURL != link.OriginalURL {
		t.Fatalf("cached=%+v hit=%v err=%v", cached, hit, err)
	}
	ttl, err := client.PTTL(t.Context(), "shortener:link:v1:"+link.Code).Result()
	if err != nil || ttl < 59*time.Minute || ttl > time.Hour {
		t.Fatalf("TTL=%v err=%v", ttl, err)
	}
	if _, err := pool.Exec(t.Context(), "ALTER TABLE links RENAME COLUMN code TO unavailable_code"); err != nil {
		t.Fatal(err)
	}
	status, _, _ = redirectRequest(t, server, http.MethodGet, link.Code, "")
	if status != 302 || visitCount(t, pool) != 2 {
		t.Fatalf("cache hit failed, status=%d", status)
	}
}

func TestCacheTTLExpiresAndRefills(t *testing.T) {
	pool, client, link := cacheFixture(t)
	cache := rediscache.NewLinkCache(client, 250*time.Millisecond)
	if err := cache.Set(t.Context(), link); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := cache.Get(t.Context(), link.Code); err != nil || !hit {
		t.Fatalf("hit=%v err=%v", hit, err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, hit, err := cache.Get(t.Context(), link.Code)
		if err != nil {
			t.Fatal(err)
		}
		if !hit {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache entry did not expire")
		}
		time.Sleep(10 * time.Millisecond)
	}
	server := cachedServer(t, pool, cache, io.Discard)
	if status, _, _ := redirectRequest(t, server, http.MethodGet, link.Code, ""); status != 302 {
		t.Fatalf("status=%d", status)
	}
	if _, hit, err := cache.Get(t.Context(), link.Code); err != nil || !hit {
		t.Fatalf("refill failed: hit=%v err=%v", hit, err)
	}
}

func TestCorruptCacheFallsBackAndIsReplaced(t *testing.T) {
	pool, client, link := cacheFixture(t)
	cache := rediscache.NewLinkCache(client, time.Hour)
	var logs bytes.Buffer
	server := cachedServer(t, pool, cache, &logs)
	for _, payload := range []string{`broken`, `{}`, `{"id":-1,"url":"https://example.org"}`, `{"id":1,"url":"javascript:private-secret"}`, `{"id":1,"url":"https://user:private-secret@example.org"}`} {
		if err := client.Set(t.Context(), "shortener:link:v1:"+link.Code, payload, time.Hour).Err(); err != nil {
			t.Fatal(err)
		}
		if status, _, _ := redirectRequest(t, server, http.MethodGet, link.Code, ""); status != 302 {
			t.Fatalf("status=%d", status)
		}
		cached, hit, err := cache.Get(t.Context(), link.Code)
		if err != nil || !hit || cached.ID != link.ID || cached.OriginalURL != link.OriginalURL {
			t.Fatalf("replacement failed: %+v %v", cached, err)
		}
	}
	server.Close()
	if strings.Contains(logs.String(), "private-secret") {
		t.Fatal("cache payload exposed in logs")
	}
}

func TestCacheHitSurvivesPostgresOutage(t *testing.T) {
	pool, client, link := cacheFixture(t)
	var logs bytes.Buffer
	server := cachedServer(t, pool, rediscache.NewLinkCache(client, time.Hour), &logs)
	if status, _, _ := redirectRequest(t, server, http.MethodGet, link.Code, ""); status != 302 {
		t.Fatalf("status=%d", status)
	}
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
	status, headers, _ := redirectRequest(t, server, http.MethodGet, link.Code, "")
	server.Close()
	if status != 302 || headers.Get("Location") != link.OriginalURL {
		t.Fatalf("status=%d headers=%v", status, headers)
	}
	if !strings.Contains(logs.String(), "visit_write_failed") || strings.Contains(logs.String(), "private-secret") {
		t.Fatalf("unsafe or missing failure log: %s", &logs)
	}
	if _, err := admin.Exec(t.Context(), "ALTER DATABASE "+identifier+" ALLOW_CONNECTIONS true"); err != nil {
		t.Fatal(err)
	}
	if visitCount(t, pool) != 1 {
		t.Fatal("failed visit write changed the event count")
	}
}

func pauseRedis(t *testing.T) func() {
	t.Helper()
	control := realRedis(t)
	if err := control.Do(t.Context(), "CLIENT", "PAUSE", 500, "ALL").Err(); err != nil {
		t.Fatal(err)
	}
	return func() {
		// Wait for the finite pause to expire before key cleanup and other tests.
		deadline := time.Now().Add(2 * time.Second)
		for {
			if err := control.Ping(t.Context()).Err(); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Error("Redis did not recover after the test pause")
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestRedisTimeoutFallsBackToPostgres(t *testing.T) {
	pool, client, link := cacheFixture(t)
	cache := rediscache.NewLinkCache(client, time.Hour)
	server := cachedServer(t, pool, cache, io.Discard)
	resume := pauseRedis(t)
	defer resume()
	started := time.Now()
	if _, _, err := cache.Get(t.Context(), link.Code); err == nil {
		t.Fatal("expected read timeout")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("read exceeded timeout budget: %v", elapsed)
	}
	started = time.Now()
	if err := cache.Set(t.Context(), link); err == nil {
		t.Fatal("expected write timeout")
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("write exceeded timeout budget: %v", elapsed)
	}
	status, _, _ := redirectRequest(t, server, http.MethodGet, link.Code, "")
	if status != 302 || visitCount(t, pool) != 1 {
		t.Fatalf("Redis outage blocked redirect, status=%d", status)
	}
}

func TestCacheFillFailureDoesNotBlockRedirect(t *testing.T) {
	pool, admin, link := cacheFixture(t)
	user := "test_" + link.Code
	if err := admin.Do(t.Context(), "ACL", "SETUSER", user, "on", ">private-test-password", "~shortener:link:v1:"+link.Code, "+get", "+ping", "+hello").Err(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := admin.Do(context.Background(), "ACL", "DELUSER", user).Err(); err != nil {
			t.Error(err)
		}
	})
	client := redisinfra.NewClient(config.Redis{Addr: os.Getenv("TEST_REDIS_ADDR"), Username: user, Password: "private-test-password"})
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Ping(t.Context()).Err(); err != nil {
		t.Fatalf("read-only Redis credentials failed: %v", err)
	}
	if err := client.Get(t.Context(), "shortener:link:v1:"+link.Code).Err(); err != redis.Nil {
		t.Fatalf("read-only client cannot read cache miss: %v", err)
	}
	server := cachedServer(t, pool, rediscache.NewLinkCache(client, time.Hour), io.Discard)
	status, _, _ := redirectRequest(t, server, http.MethodGet, link.Code, "")
	if status != 302 || visitCount(t, pool) != 1 {
		t.Fatalf("cache fill error blocked redirect: %d", status)
	}
	if count, err := admin.Exists(t.Context(), "shortener:link:v1:"+link.Code).Result(); err != nil || count != 0 {
		t.Fatalf("unexpected cache entry: count=%d err=%v", count, err)
	}
}

func TestMissingLinkIsNotCached(t *testing.T) {
	pool, client, link := cacheFixture(t)
	if _, err := pool.Exec(t.Context(), "DELETE FROM links WHERE id=$1", link.ID); err != nil {
		t.Fatal(err)
	}
	server := cachedServer(t, pool, rediscache.NewLinkCache(client, time.Hour), io.Discard)
	if status, _, _ := redirectRequest(t, server, http.MethodGet, link.Code, ""); status != 404 {
		t.Fatalf("status=%d", status)
	}
	if count, err := client.Exists(t.Context(), "shortener:link:v1:"+link.Code).Result(); err != nil || count != 0 {
		t.Fatalf("negative cache entry: count=%d err=%v", count, err)
	}
}

func TestUnicodeRedirectThroughPostgresAndCache(t *testing.T) {
	pool, client, link := cacheFixture(t)
	seen := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.RequestURI
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()
	original := target.URL + "/путь/😀?q=поиск&encoded=a%20b#якорь"
	if _, err := pool.Exec(t.Context(), "UPDATE links SET original_url=$1 WHERE id=$2", original, link.ID); err != nil {
		t.Fatal(err)
	}
	cache := rediscache.NewLinkCache(client, time.Hour)
	server := cachedServer(t, pool, cache, io.Discard)
	status, headers, _ := redirectRequest(t, server, http.MethodGet, link.Code, "")
	if status != 302 {
		t.Fatalf("status=%d", status)
	}
	location := headers.Get("Location")
	for _, char := range location {
		if char > 127 {
			t.Fatalf("non-ASCII Location: %q", location)
		}
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.Path != "/путь/😀" || parsed.Query().Get("q") != "поиск" || parsed.Query().Get("encoded") != "a b" || parsed.Fragment != "якорь" {
		t.Fatalf("invalid Unicode redirect %q: %v", location, err)
	}
	cached, hit, err := cache.Get(t.Context(), link.Code)
	if err != nil || !hit || cached.OriginalURL != original {
		t.Fatalf("Unicode cache round trip failed: %+v err=%v", cached, err)
	}
	follow := &http.Client{Timeout: 5 * time.Second}
	response, err := follow.Get(server.URL + "/s/" + link.Code)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatalf("followed status=%d", response.StatusCode)
	}
	select {
	case uri := <-seen:
		requestURL, err := url.ParseRequestURI(uri)
		if err != nil || requestURL.Path != "/путь/😀" || requestURL.Query().Get("q") != "поиск" {
			t.Fatalf("wrong target request %q", uri)
		}
	default:
		t.Fatal("redirect did not reach target")
	}
	if visitCount(t, pool) != 2 {
		t.Fatal("unexpected visit count")
	}
	data, err := client.Get(t.Context(), "shortener:link:v1:"+link.Code).Bytes()
	if err != nil || !json.Valid(data) {
		t.Fatal("invalid stored cache data")
	}
}
