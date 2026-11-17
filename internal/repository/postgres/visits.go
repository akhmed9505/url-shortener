package postgres

import (
	"context"
	"fmt"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

// VisitRepository records link visits.
type VisitRepository struct {
	pool *pgxpool.Pool
}

// NewVisitRepository creates a PostgreSQL visit repository.
func NewVisitRepository(pool *pgxpool.Pool) *VisitRepository {
	return &VisitRepository{pool: pool}
}

// Record stores one visit event.
func (r *VisitRepository) Record(ctx context.Context, visit domain.Visit) error {
	const query = `INSERT INTO visits (link_id, visited_at, user_agent) VALUES ($1, $2, $3)`
	_, err := r.pool.Exec(ctx, query, visit.LinkID, visit.VisitedAt.UTC(), visit.UserAgent)
	if err != nil {
		return fmt.Errorf("record visit: %w", storageError(err))
	}
	return nil
}
