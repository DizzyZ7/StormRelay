package storage

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestInvalidCreationErrorsAreDistinctFromAuditFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "explicit validation", err: fmt.Errorf("wrap: %w", ErrInvalidCreateInput), want: true},
		{name: "duplicate", err: fmt.Errorf("create: %w", &pgconn.PgError{Code: "23505"}), want: true},
		{name: "check constraint", err: &pgconn.PgError{Code: "23514"}, want: true},
		{name: "invalid JSON", err: &pgconn.PgError{Code: "22P02"}, want: true},
		{name: "audit trigger failure", err: fmt.Errorf("audit: %w", &pgconn.PgError{Code: "P0001"}), want: false},
		{name: "database unavailable", err: errors.New("connection refused"), want: false},
		{name: "no error", err: nil, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInvalidCreateInput(tc.err); got != tc.want {
				t.Fatalf("IsInvalidCreateInput(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}
