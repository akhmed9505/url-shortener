//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/akhmed9505/url-shortener/internal/app"
	"github.com/akhmed9505/url-shortener/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type applicationLogs struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *applicationLogs) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}
func (b *applicationLogs) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

type runningApplication struct {
	url, name string
	cancel    context.CancelFunc
	done      chan struct{}
	err       error
	logs      applicationLogs
}

func startApplication(t *testing.T, pool *pgxpool.Pool, redisConfig config.Redis) *runningApplication {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	state := &runningApplication{url: "http://" + addr, name: pool.Config().ConnConfig.Database + "_app", done: make(chan struct{})}
	dsn, err := url.Parse(os.Getenv("TEST_POSTGRES_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	dsn.Path = "/" + pool.Config().ConnConfig.Database
	query := dsn.Query()
	query.Set("application_name", state.name)
	dsn.RawQuery = query.Encode()
	cfg := config.Config{
		HTTP:     config.HTTP{Addr: addr, ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: time.Second},
		Postgres: config.Postgres{DSN: dsn.String(), MaxConns: 4, ConnectTimeout: time.Second},
		Redis:    redisConfig, PublicBaseURL: state.url, AnalyticsWriteTimeout: 150 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(t.Context())
	state.cancel = cancel
	go func() {
		state.err = app.New(cfg, slog.New(slog.NewJSONHandler(&state.logs, nil))).Run(ctx)
		close(state.done)
	}()
	t.Cleanup(func() {
		state.cancel()
		select {
		case <-state.done:
			if state.err != nil {
				t.Errorf("application error: %v", state.err)
			}
		case <-time.After(12 * time.Second):
			t.Error("application did not stop within shutdown budget")
		}
	})
	waitUntil(t, 5*time.Second, func() bool {
		select {
		case <-state.done:
			t.Fatalf("application exited during startup: %v", state.err)
		default:
		}
		client := &http.Client{Timeout: 100 * time.Millisecond}
		response, err := client.Get(state.url + "/health/live")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		_, _ = io.Copy(io.Discard, response.Body)
		return response.StatusCode == 200
	})
	return state
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}
}

func redisIDs(t *testing.T, client *redis.Client) map[string]bool {
	t.Helper()
	result, err := client.ClientList(t.Context()).Result()
	if err != nil {
		t.Fatal(err)
	}
	ids := make(map[string]bool)
	for _, line := range strings.Split(result, "\n") {
		for _, field := range strings.Fields(line) {
			if id, ok := strings.CutPrefix(field, "id="); ok {
				ids[id] = true
			}
		}
	}
	return ids
}

func TestApplicationDrainsRequestsBeforeClosingResources(t *testing.T) {
	pool, _ := newDatabase(t, true)
	redisClient := realRedis(t)
	before := redisIDs(t, redisClient)
	state := startApplication(t, pool, config.Redis{Addr: redisClient.Options().Addr, CacheTTL: time.Hour})
	checkHealthStatus(t, state.url, "/health/ready", 200)
	appIDs := redisIDs(t, redisClient)
	for id := range before {
		delete(appIDs, id)
	}
	if len(appIDs) == 0 {
		t.Fatal("application Redis connection was not observed")
	}
	blocker, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback(context.Background())
	if _, err := blocker.Exec(t.Context(), "LOCK TABLE links IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		status int
		err    error
	}
	responseDone := make(chan result, 1)
	go func() {
		request, err := http.NewRequest("POST", state.url+"/shorten", strings.NewReader(`{"url":"https://private-target.example/path?token=private-token","alias":"draining"}`))
		if err != nil {
			responseDone <- result{err: err}
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("User-Agent", "private-agent")
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			responseDone <- result{err: err}
			return
		}
		defer response.Body.Close()
		_, err = io.Copy(io.Discard, response.Body)
		responseDone <- result{response.StatusCode, err}
	}()
	waitUntil(t, time.Second, func() bool {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE 'INSERT INTO links%')`, state.name).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		return waiting
	})
	state.cancel()
	waitUntil(t, time.Second, func() bool { return strings.Contains(state.logs.String(), "shutting down HTTP server") })
	waitUntil(t, time.Second, func() bool {
		connection, err := net.DialTimeout("tcp", strings.TrimPrefix(state.url, "http://"), 50*time.Millisecond)
		if err != nil {
			return true
		}
		connection.Close()
		return false
	})
	select {
	case <-state.done:
		t.Fatal("application stopped while an active request was blocked")
	default:
	}
	var connections int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1", state.name).Scan(&connections); err != nil || connections == 0 {
		t.Fatalf("PostgreSQL connections during drain=%d err=%v", connections, err)
	}
	during := redisIDs(t, redisClient)
	for id := range appIDs {
		if !during[id] {
			t.Fatal("Redis closed before request completion")
		}
	}
	if err := blocker.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	response := <-responseDone
	if response.err != nil || response.status != 201 {
		t.Fatalf("active request=%+v", response)
	}
	select {
	case <-state.done:
	case <-time.After(5 * time.Second):
		t.Fatal("application did not finish shutdown")
	}
	if state.err != nil {
		t.Fatal(state.err)
	}
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM pg_stat_activity WHERE application_name=$1", state.name).Scan(&connections); err != nil || connections != 0 {
		t.Fatalf("PostgreSQL connections after shutdown=%d err=%v", connections, err)
	}
	after := redisIDs(t, redisClient)
	for id := range appIDs {
		if after[id] {
			t.Fatal("Redis connection remained after shutdown")
		}
	}
	for _, secret := range []string{"private-target", "private-token", "private-agent"} {
		if strings.Contains(state.logs.String(), secret) {
			t.Fatalf("sensitive data in logs: %s", secret)
		}
	}
	decoder := json.NewDecoder(strings.NewReader(state.logs.String()))
	found := false
	for {
		var entry map[string]any
		err := decoder.Decode(&entry)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if entry["msg"] == "http_request" && entry["method"] == "POST" {
			found = true
			if entry["route_pattern"] != "/shorten" || entry["status"] != float64(201) {
				t.Fatalf("access log=%v", entry)
			}
		}
	}
	if !found {
		t.Fatal("missing completed request access log")
	}
}

func TestApplicationReadinessWithUnavailableRedis(t *testing.T) {
	pool, _ := newDatabase(t, true)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	state := startApplication(t, pool, config.Redis{Addr: addr, CacheTTL: time.Hour})
	checkHealthStatus(t, state.url, "/health/ready", 200)
	if !strings.Contains(state.logs.String(), "redis_unavailable") {
		t.Fatal("missing optional Redis startup warning")
	}
}
