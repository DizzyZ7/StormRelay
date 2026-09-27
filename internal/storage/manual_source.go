package storage

import (
	"context"
	"fmt"

	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
)

// EnsureManualSource provisions a tenant-private source used only by the authenticated
// manual incident API. The unique (tenant_id, name) constraint serializes competing
// first requests; the follow-up SELECT observes the winner without overwriting it.
func (s *Store) EnsureManualSource(ctx context.Context, tenantID string) (string, error) {
	if tenantID == "" {
		return "", fmt.Errorf("manual source requires a tenant")
	}
	sourceID, err := id.New()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO event_sources
		(id, tenant_id, name, kind, auth_mode, enabled, rate_limit_per_second, rate_limit_burst)
		VALUES ($1, $2, 'manual-api', 'generic', 'none', true, 20, 40)
		ON CONFLICT (tenant_id, name) DO NOTHING`, sourceID, tenantID)
	if err != nil {
		return "", fmt.Errorf("provision manual source: %w", err)
	}
	var actualID, kind string
	var authMode ingestion.AuthMode
	var enabled bool
	err = s.pool.QueryRow(ctx, `SELECT id, kind, auth_mode, enabled
		FROM event_sources WHERE tenant_id=$1 AND name='manual-api'`, tenantID).
		Scan(&actualID, &kind, &authMode, &enabled)
	if err != nil {
		return "", fmt.Errorf("read manual source: %w", err)
	}
	if kind != "generic" || authMode != ingestion.AuthNone || !enabled {
		return "", fmt.Errorf("manual source for tenant is not in the expected state")
	}
	return actualID, nil
}
