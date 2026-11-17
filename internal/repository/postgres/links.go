package postgres

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// LinkRepository stores links and resolves short codes.
type LinkRepository struct {
	pool *pgxpool.Pool
}

// NewLinkRepository creates a PostgreSQL link repository.
func NewLinkRepository(pool *pgxpool.Pool) *LinkRepository {
	return &LinkRepository{pool: pool}
}

// Create atomically stores a link or reports a short code conflict.
func (r *LinkRepository) Create(ctx context.Context, code, originalURL string) (domain.Link, error) {
	const query = `INSERT INTO links (code, original_url)
		VALUES ($1, $2)
		ON CONFLICT (code) DO NOTHING
		RETURNING id, code, original_url, created_at`
	var link domain.Link
	err := r.pool.QueryRow(ctx, query, code, originalURL).Scan(
		&link.ID, &link.Code, &link.OriginalURL, &link.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, domain.ErrCodeConflict
	}
	if err != nil {
		return domain.Link{}, fmt.Errorf("create link: %w", storageError(err))
	}
	link.CreatedAt = link.CreatedAt.UTC()
	return link, nil
}

// FindByCode returns the stored link or a not-found error.
func (r *LinkRepository) FindByCode(ctx context.Context, code string) (domain.Link, error) {
	const query = `SELECT id, code, original_url, created_at FROM links WHERE code = $1`
	var link domain.Link
	err := r.pool.QueryRow(ctx, query, code).Scan(
		&link.ID, &link.Code, &link.OriginalURL, &link.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Link{}, domain.ErrLinkNotFound
	}
	if err != nil {
		return domain.Link{}, fmt.Errorf("find link: %w", storageError(err))
	}
	link.CreatedAt = link.CreatedAt.UTC()
	return link, nil
}

func storageError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) {
		return fmt.Errorf("%w: %w", domain.ErrStorageUnavailable, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if strings.HasPrefix(pgErr.Code, "08") || pgErr.Code == "57P01" ||
			pgErr.Code == "57P02" || pgErr.Code == "57P03" || pgErr.Code == "53300" {
			return fmt.Errorf("%w: %w", domain.ErrStorageUnavailable, err)
		}
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%w: %w", domain.ErrStorageUnavailable, err)
	}
	return err
}
