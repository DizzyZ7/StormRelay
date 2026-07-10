package storage

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateSource(ctx context.Context, in CreateSourceInput) (CreateSourceResult, error) {
	if strings.TrimSpace(in.Name) == "" {
		return CreateSourceResult{}, fmt.Errorf("source name is required")
	}
	if in.Kind == "" {
		in.Kind = "generic"
	}
	if in.RateLimitPerSecond <= 0 {
		in.RateLimitPerSecond = 20
	}
	if in.RateLimitBurst <= 0 {
		in.RateLimitBurst = 40
	}
	if in.AuthMode == "" {
		in.AuthMode = ingestion.AuthHMAC
	}
	sourceID, err := id.New()
	if err != nil {
		return CreateSourceResult{}, err
	}
	credential := ""
	var encrypted, hash []byte
	switch in.AuthMode {
	case ingestion.AuthHMAC:
		credential, err = randomSecret(32)
		if err != nil {
			return CreateSourceResult{}, err
		}
		encrypted, err = s.box.Encrypt([]byte(credential), []byte(sourceID))
		if err != nil {
			return CreateSourceResult{}, err
		}
	case ingestion.AuthBearer:
		credential, err = randomSecret(32)
		if err != nil {
			return CreateSourceResult{}, err
		}
		h := cryptox.HashSecret(credential)
		hash = h[:]
	case ingestion.AuthNone:
	default:
		return CreateSourceResult{}, fmt.Errorf("unsupported auth mode %q", in.AuthMode)
	}
	var out Source
	err = s.pool.QueryRow(ctx, `INSERT INTO event_sources(id,tenant_id,name,kind,auth_mode,encrypted_secret,bearer_hash,rate_limit_per_second,rate_limit_burst)
	VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id,tenant_id,name,kind,auth_mode,enabled,rate_limit_per_second,rate_limit_burst,created_at,version`,
		sourceID, in.TenantID, in.Name, in.Kind, in.AuthMode, encrypted, hash, in.RateLimitPerSecond, in.RateLimitBurst).Scan(&out.ID, &out.TenantID, &out.Name, &out.Kind, &out.AuthMode, &out.Enabled, &out.RateLimitPerSecond, &out.RateLimitBurst, &out.CreatedAt, &out.Version)
	if err != nil {
		return CreateSourceResult{}, fmt.Errorf("create source: %w", err)
	}
	return CreateSourceResult{Source: out, Credential: credential}, nil
}

func (s *Store) ListSources(ctx context.Context, tenantID string, limit int) ([]Source, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,name,kind,auth_mode,enabled,rate_limit_per_second,rate_limit_burst,created_at,version FROM event_sources WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT $2`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Source{}
	for rows.Next() {
		var x Source
		if err := rows.Scan(&x.ID, &x.TenantID, &x.Name, &x.Kind, &x.AuthMode, &x.Enabled, &x.RateLimitPerSecond, &x.RateLimitBurst, &x.CreatedAt, &x.Version); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) GetSourceCredentials(ctx context.Context, sourceID string) (SourceCredentials, error) {
	var out SourceCredentials
	var encrypted []byte
	err := s.pool.QueryRow(ctx, `SELECT id,tenant_id,name,kind,auth_mode,enabled,rate_limit_per_second,rate_limit_burst,created_at,version,encrypted_secret,bearer_hash FROM event_sources WHERE id=$1`, sourceID).Scan(&out.Source.ID, &out.Source.TenantID, &out.Source.Name, &out.Source.Kind, &out.Source.AuthMode, &out.Source.Enabled, &out.Source.RateLimitPerSecond, &out.Source.RateLimitBurst, &out.Source.CreatedAt, &out.Source.Version, &encrypted, &out.BearerHash)
	if err != nil {
		if err == pgx.ErrNoRows {
			return SourceCredentials{}, errNoRows
		}
		return SourceCredentials{}, err
	}
	if len(encrypted) > 0 {
		out.HMACSecret, err = s.box.Decrypt(encrypted, []byte(sourceID))
		if err != nil {
			return SourceCredentials{}, fmt.Errorf("decrypt source secret: %w", err)
		}
	}
	return out, nil
}

func randomSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
