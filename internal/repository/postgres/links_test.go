package postgres

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/akhmed9505/url-shortener/internal/domain"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestStorageError(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		unavailable bool
	}{
		{"shutdown", &pgconn.PgError{Code: "57P01"}, true},
		{"connection", &pgconn.PgError{Code: "08006"}, true},
		{"capacity", &pgconn.PgError{Code: "53300"}, true},
		{"check violation", &pgconn.PgError{Code: "23514"}, false},
		{"other unique violation", &pgconn.PgError{Code: "23505"}, false},
		{"missing table", &pgconn.PgError{Code: "42P01"}, false},
		{"connection closed", io.EOF, true},
		{"connection interrupted", io.ErrUnexpectedEOF, true},
		{"canceled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := storageError(tc.err)
			if errors.Is(got, domain.ErrStorageUnavailable) != tc.unavailable {
				t.Fatalf("unexpected classification: %v", got)
			}
			if !errors.Is(got, tc.err) {
				t.Fatal("original error was not preserved")
			}
		})
	}
}
