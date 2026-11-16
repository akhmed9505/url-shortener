package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AnalyticsRepository struct{ pool *pgxpool.Pool }

func NewAnalyticsRepository(pool *pgxpool.Pool) *AnalyticsRepository {
	return &AnalyticsRepository{pool: pool}
}

func (r *AnalyticsRepository) Summary(ctx context.Context, code string, period domain.AnalyticsPeriod) (domain.Analytics, error) {
	result, err := r.summary(ctx, code, period)
	if err != nil {
		return domain.Analytics{}, fmt.Errorf("read analytics: %w", storageError(err))
	}
	return result, nil
}

func (r *AnalyticsRepository) summary(ctx context.Context, code string, period domain.AnalyticsPeriod) (domain.Analytics, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return domain.Analytics{}, err
	}
	defer tx.Rollback(ctx)
	result := domain.Analytics{Period: period}
	var linkID int64
	err = tx.QueryRow(ctx, `SELECT id, code, original_url FROM links WHERE code=$1`, code).Scan(&linkID, &result.Code, &result.URL)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, domain.ErrLinkNotFound
	}
	if err != nil {
		return result, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE visited_at >= $2 AND visited_at < $3)
 FROM visits WHERE link_id=$1`, linkID, period.From, period.To).Scan(&result.TotalVisits, &result.PeriodVisits)
	if err != nil {
		return result, err
	}
	rows, err := tx.Query(ctx, `WITH counts AS (
 SELECT (visited_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n FROM visits
 WHERE link_id=$1 AND visited_at >= $2 AND visited_at < $3 GROUP BY 1)
 SELECT to_char(days.day, 'YYYY-MM-DD'), coalesce(counts.n,0)::bigint
 FROM generate_series(($2::timestamptz AT TIME ZONE 'UTC')::timestamp,
 ($3::timestamptz AT TIME ZONE 'UTC')::timestamp - interval '1 day', interval '1 day') AS days(day)
 LEFT JOIN counts ON counts.day=days.day ORDER BY days.day`, linkID, period.From, period.To)
	if err != nil {
		return result, err
	}
	result.Daily, err = pgx.CollectRows(rows, pgx.RowToStructByPos[domain.DailyVisits])
	if err != nil {
		return result, err
	}
	rows, err = tx.Query(ctx, `WITH counts AS (
 SELECT date_trunc('month', visited_at AT TIME ZONE 'UTC') AS month, count(*) AS n FROM visits
 WHERE link_id=$1 AND visited_at >= $2 AND visited_at < $3 GROUP BY 1)
 SELECT to_char(months.month,'YYYY-MM'), coalesce(counts.n,0)::bigint
 FROM generate_series(date_trunc('month',$2::timestamptz AT TIME ZONE 'UTC'),
 date_trunc('month',($3::timestamptz AT TIME ZONE 'UTC') - interval '1 day'), interval '1 month') AS months(month)
 LEFT JOIN counts ON counts.month=months.month ORDER BY months.month`, linkID, period.From, period.To)
	if err != nil {
		return result, err
	}
	result.Monthly, err = pgx.CollectRows(rows, pgx.RowToStructByPos[domain.MonthlyVisits])
	if err != nil {
		return result, err
	}
	rows, err = tx.Query(ctx, `SELECT user_agent COLLATE "C", count(*) AS n FROM visits
 WHERE link_id=$1 AND visited_at >= $2 AND visited_at < $3
 GROUP BY user_agent COLLATE "C" ORDER BY n DESC, user_agent COLLATE "C" ASC LIMIT 20`, linkID, period.From, period.To)
	if err != nil {
		return result, err
	}
	result.UserAgents, err = pgx.CollectRows(rows, pgx.RowToStructByPos[domain.UserAgentVisits])
	if err != nil {
		return result, err
	}
	if result.UserAgents == nil {
		result.UserAgents = []domain.UserAgentVisits{}
	}
	result.OtherUserAgentsVisits = result.PeriodVisits
	for _, agent := range result.UserAgents {
		result.OtherUserAgentsVisits -= agent.Count
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	return result, nil
}
