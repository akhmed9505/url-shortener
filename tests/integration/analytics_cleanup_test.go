//go:build integration

package integration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	pgrepo "github.com/akhmed9505/url-shortener/internal/repository/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type rollbackObservation struct {
	err       error
	remaining time.Duration
	hasLimit  bool
}

type cancelAnalyticsTracer struct {
	cancel   context.CancelFunc
	rollback chan rollbackObservation
}

func (t *cancelAnalyticsTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(data.SQL, "SELECT count(*), count(*) FILTER") {
		t.cancel()
	}
	if data.SQL == "rollback" {
		deadline, ok := ctx.Deadline()
		t.rollback <- rollbackObservation{err: ctx.Err(), remaining: time.Until(deadline), hasLimit: ok}
	}
	return ctx
}

func (*cancelAnalyticsTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestAnalyticsRollbackHasIndependentDeadline(t *testing.T) {
	pool, _ := newDatabase(t, true)
	link := createRedirectLink(t, pool)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	tracer := &cancelAnalyticsTracer{cancel: cancel, rollback: make(chan rollbackObservation, 1)}
	cfg := pool.Config()
	cfg.MaxConns = 1
	cfg.ConnConfig.Tracer = tracer
	tracedPool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tracedPool.Close()
	_, err = pgrepo.NewAnalyticsRepository(tracedPool).Summary(ctx, link.Code, analyticsPeriod("2024-02-29", "2024-03-01"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("summary error=%v", err)
	}
	select {
	case cleanup := <-tracer.rollback:
		if cleanup.err != nil || !cleanup.hasLimit || cleanup.remaining <= 0 || cleanup.remaining > time.Second {
			t.Fatalf("rollback context=%+v", cleanup)
		}
	default:
		t.Fatal("rollback was not attempted")
	}
	var value int
	if err := tracedPool.QueryRow(t.Context(), "SELECT 1").Scan(&value); err != nil || value != 1 {
		t.Fatalf("pool reuse after cancellation: value=%d err=%v", value, err)
	}
	var idleTransactions int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM pg_stat_activity
		WHERE datname=current_database() AND state LIKE 'idle in transaction%'`).Scan(&idleTransactions); err != nil || idleTransactions != 0 {
		t.Fatalf("idle transactions=%d err=%v", idleTransactions, err)
	}
}
