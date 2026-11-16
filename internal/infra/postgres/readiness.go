package postgres

import (
	"context"
	"errors"
	"slices"

	"github.com/akhmed9505/url-shortener/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CheckReadiness checks connectivity and applied Goose versions without modifying the database.
func CheckReadiness(ctx context.Context, pool *pgxpool.Pool) error {
	expected, err := migrations.Versions()
	if err != nil {
		return err
	}
	const query = `SELECT version_id FROM (
		SELECT DISTINCT ON (version_id) version_id, is_applied
		FROM goose_db_version ORDER BY version_id, id DESC
	) AS latest WHERE is_applied AND version_id > 0 ORDER BY version_id`
	rows, err := pool.Query(ctx, query)
	if err != nil {
		return err
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	if !slices.Equal(applied, expected) {
		return errors.New("database migrations are not current")
	}
	// LIMIT 0 checks required relations and columns without reading application data.
	_, err = pool.Exec(ctx, `SELECT l.id, l.code, l.original_url, l.created_at,
  v.id, v.link_id, v.visited_at, v.user_agent FROM links l CROSS JOIN visits v LIMIT 0`)
	return err
}
