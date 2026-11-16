package postgres

import (
	"context"
	"fmt"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/jackc/pgx/v5"
)

// ListVisits returns at most limit rows. The service requests one extra row to detect the next page.
func (r *AnalyticsRepository) ListVisits(ctx context.Context, code string, period domain.AnalyticsPeriod, after *domain.VisitPosition, limit int) ([]domain.Visit, error) {
	link, err := NewLinkRepository(r.pool).FindByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, link_id, visited_at, user_agent FROM visits
		WHERE link_id=$1 AND visited_at >= $2 AND visited_at < $3`
	args := []any{link.ID, period.From, period.To}
	if after != nil {
		query += ` AND (visited_at, id) < ($4, $5) ORDER BY visited_at DESC, id DESC LIMIT $6`
		args = append(args, after.VisitedAt, after.ID, limit)
	} else {
		query += ` ORDER BY visited_at DESC, id DESC LIMIT $4`
		args = append(args, limit)
	}
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list visits: %w", storageError(err))
	}
	visits, err := pgx.CollectRows(rows, pgx.RowToStructByPos[domain.Visit])
	if err != nil {
		return nil, fmt.Errorf("list visits: %w", storageError(err))
	}
	for i := range visits {
		visits[i].VisitedAt = visits[i].VisitedAt.UTC()
	}
	return visits, nil
}
