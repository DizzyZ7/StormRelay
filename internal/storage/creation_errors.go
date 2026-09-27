package storage

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrInvalidCreateInput marks caller-provided resource fields that cannot be
// accepted. It is distinct from an audit or transaction failure.
var ErrInvalidCreateInput = errors.New("invalid resource creation input")

// IsInvalidCreateInput covers explicit validation and PostgreSQL data or
// uniqueness constraints. Audit/commit failures must not be reported as bad
// client input, since retrying after an uncertain commit requires care.
func IsInvalidCreateInput(err error) bool {
	if errors.Is(err, ErrInvalidCreateInput) {
		return true
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "23503", "23505", "23514", "22P02", "22001", "22023":
		return true
	default:
		return false
	}
}
