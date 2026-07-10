package storage

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/jackc/pgx/v5"
)

type OIDCProvider struct {
	ID                   string    `json:"id"`
	TenantID             string    `json:"tenant_id"`
	Name                 string    `json:"name"`
	Issuer               string    `json:"issuer"`
	Audience             string    `json:"audience"`
	JWKSURI              string    `json:"jwks_uri"`
	SupportedSigningAlgs []string  `json:"supported_signing_algs"`
	Enabled              bool      `json:"enabled"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
	Version              int64     `json:"version"`
}

type OIDCIdentity struct {
	ID          string      `json:"id"`
	TenantID    string      `json:"tenant_id"`
	ProviderID  string      `json:"provider_id"`
	UserID      string      `json:"user_id"`
	Subject     string      `json:"subject"`
	Email       string      `json:"email"`
	DisplayName string      `json:"display_name"`
	Enabled     bool        `json:"enabled"`
	Roles       []auth.Role `json:"roles"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Version     int64       `json:"version"`
}

type CreateOIDCProviderInput struct {
	TenantID, Name, Issuer, Audience, JWKSURI, ActorID, RequestID, TraceID string
	SupportedSigningAlgs                                                   []string
}

type UpdateOIDCProviderInput struct {
	TenantID, ID, Name, ActorID, RequestID, TraceID string
	Enabled                                         bool
	ExpectedVersion                                 int64
}

type CreateOIDCIdentityInput struct {
	TenantID, ProviderID, Subject, Email, DisplayName, ActorID, RequestID, TraceID string
	Roles                                                                          []auth.Role
}

type UpdateOIDCIdentityInput struct {
	TenantID, ID, ActorID, RequestID, TraceID string
	Enabled                                    bool
	Roles                                      []auth.Role
	ExpectedVersion                            int64
}

func (s *Store) CreateOIDCProvider(ctx context.Context, in CreateOIDCProviderInput) (OIDCProvider, error) {
	name := strings.TrimSpace(in.Name)
	issuer := strings.TrimSpace(in.Issuer)
	audience := strings.TrimSpace(in.Audience)
	jwksURI := strings.TrimSpace(in.JWKSURI)
	if name == "" || len(name) > 200 {
		return OIDCProvider{}, fmt.Errorf("provider name must contain 1 to 200 bytes")
	}
	if err := validateHTTPSURL(issuer); err != nil {
		return OIDCProvider{}, fmt.Errorf("issuer: %w", err)
	}
	if audience == "" || len(audience) > 500 {
		return OIDCProvider{}, fmt.Errorf("audience must contain 1 to 500 bytes")
	}
	if err := validateHTTPSURL(jwksURI); err != nil {
		return OIDCProvider{}, fmt.Errorf("jwks URI: %w", err)
	}
	algs := normalizeSigningAlgs(in.SupportedSigningAlgs)
	if len(algs) == 0 {
		return OIDCProvider{}, fmt.Errorf("at least one supported signing algorithm is required")
	}
	providerID, err := id.New()
	if err != nil {
		return OIDCProvider{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OIDCProvider{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var out OIDCProvider
	err = tx.QueryRow(ctx, `
		INSERT INTO oidc_providers(id,tenant_id,name,issuer,audience,jwks_uri,supported_signing_algs)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		RETURNING id,tenant_id,name,issuer,audience,jwks_uri,supported_signing_algs,enabled,created_at,updated_at,version`,
		providerID, in.TenantID, name, issuer, audience, jwksURI, algs,
	).Scan(&out.ID, &out.TenantID, &out.Name, &out.Issuer, &out.Audience, &out.JWKSURI, &out.SupportedSigningAlgs, &out.Enabled, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err != nil {
		return OIDCProvider{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{
		TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"),
		Action: "oidc_provider.created", ResourceType: "oidc_provider", ResourceID: providerID,
		RequestID: in.RequestID, TraceID: in.TraceID, After: out,
		Metadata: map[string]any{"issuer": issuer, "audience": audience, "jwks_host": hostOnly(jwksURI)},
	}); err != nil {
		return OIDCProvider{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OIDCProvider{}, err
	}
	return out, nil
}

func (s *Store) ListOIDCProviders(ctx context.Context, tenantID string) ([]OIDCProvider, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id,tenant_id,name,issuer,audience,jwks_uri,supported_signing_algs,enabled,created_at,updated_at,version
		FROM oidc_providers WHERE tenant_id=$1 ORDER BY created_at DESC,id DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OIDCProvider{}
	for rows.Next() {
		provider, err := scanOIDCProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, provider)
	}
	return out, rows.Err()
}

func (s *Store) GetOIDCProvider(ctx context.Context, tenantID, providerID string) (OIDCProvider, error) {
	provider, err := scanOIDCProvider(s.pool.QueryRow(ctx, `
		SELECT id,tenant_id,name,issuer,audience,jwks_uri,supported_signing_algs,enabled,created_at,updated_at,version
		FROM oidc_providers WHERE tenant_id=$1 AND id=$2`, tenantID, providerID))
	if err == pgx.ErrNoRows {
		return OIDCProvider{}, errNoRows
	}
	return provider, err
}

func (s *Store) FindOIDCProvider(ctx context.Context, issuer string, audiences []string) (OIDCProvider, error) {
	if len(audiences) == 0 {
		return OIDCProvider{}, errNoRows
	}
	provider, err := scanOIDCProvider(s.pool.QueryRow(ctx, `
		SELECT id,tenant_id,name,issuer,audience,jwks_uri,supported_signing_algs,enabled,created_at,updated_at,version
		FROM oidc_providers
		WHERE issuer=$1 AND audience=ANY($2::text[]) AND enabled=true`, issuer, audiences))
	if err == pgx.ErrNoRows {
		return OIDCProvider{}, errNoRows
	}
	return provider, err
}

func (s *Store) UpdateOIDCProvider(ctx context.Context, in UpdateOIDCProviderInput) (OIDCProvider, error) {
	if in.ExpectedVersion < 1 {
		return OIDCProvider{}, fmt.Errorf("expected version is required")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 200 {
		return OIDCProvider{}, fmt.Errorf("provider name must contain 1 to 200 bytes")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OIDCProvider{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	before, err := scanOIDCProvider(tx.QueryRow(ctx, `
		SELECT id,tenant_id,name,issuer,audience,jwks_uri,supported_signing_algs,enabled,created_at,updated_at,version
		FROM oidc_providers WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, in.TenantID, in.ID))
	if err == pgx.ErrNoRows {
		return OIDCProvider{}, errNoRows
	}
	if err != nil {
		return OIDCProvider{}, err
	}
	if before.Version != in.ExpectedVersion {
		return OIDCProvider{}, fmt.Errorf("version conflict: current=%d", before.Version)
	}
	out, err := scanOIDCProvider(tx.QueryRow(ctx, `
		UPDATE oidc_providers SET name=$3,enabled=$4,updated_at=now(),version=version+1
		WHERE tenant_id=$1 AND id=$2 AND version=$5
		RETURNING id,tenant_id,name,issuer,audience,jwks_uri,supported_signing_algs,enabled,created_at,updated_at,version`,
		in.TenantID, in.ID, name, in.Enabled, in.ExpectedVersion))
	if err != nil {
		return OIDCProvider{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{
		TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"),
		Action: "oidc_provider.updated", ResourceType: "oidc_provider", ResourceID: in.ID,
		RequestID: in.RequestID, TraceID: in.TraceID, Before: before, After: out,
	}); err != nil {
		return OIDCProvider{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OIDCProvider{}, err
	}
	return out, nil
}

func (s *Store) CreateOIDCIdentity(ctx context.Context, in CreateOIDCIdentityInput) (OIDCIdentity, error) {
	subject := strings.TrimSpace(in.Subject)
	email := strings.ToLower(strings.TrimSpace(in.Email))
	displayName := strings.TrimSpace(in.DisplayName)
	if subject == "" || len(subject) > 1000 {
		return OIDCIdentity{}, fmt.Errorf("subject must contain 1 to 1000 bytes")
	}
	if email == "" || len(email) > 320 || !strings.Contains(email, "@") {
		return OIDCIdentity{}, fmt.Errorf("valid email is required")
	}
	if displayName == "" || len(displayName) > 200 {
		return OIDCIdentity{}, fmt.Errorf("display name must contain 1 to 200 bytes")
	}
	roles, err := auth.NormalizeRoles(in.Roles)
	if err != nil {
		return OIDCIdentity{}, err
	}
	userID, err := id.New()
	if err != nil {
		return OIDCIdentity{}, err
	}
	identityID, err := id.New()
	if err != nil {
		return OIDCIdentity{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OIDCIdentity{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var providerExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM oidc_providers WHERE tenant_id=$1 AND id=$2)`, in.TenantID, in.ProviderID).Scan(&providerExists); err != nil {
		return OIDCIdentity{}, err
	}
	if !providerExists {
		return OIDCIdentity{}, errNoRows
	}
	if _, err := tx.Exec(ctx, `INSERT INTO users(id,tenant_id,email,display_name) VALUES($1,$2,$3,$4)`, userID, in.TenantID, email, displayName); err != nil {
		return OIDCIdentity{}, err
	}
	var out OIDCIdentity
	err = tx.QueryRow(ctx, `
		INSERT INTO oidc_identities(id,tenant_id,provider_id,user_id,subject)
		VALUES($1,$2,$3,$4,$5)
		RETURNING id,tenant_id,provider_id,user_id,subject,enabled,created_at,updated_at,version`,
		identityID, in.TenantID, in.ProviderID, userID, subject,
	).Scan(&out.ID, &out.TenantID, &out.ProviderID, &out.UserID, &out.Subject, &out.Enabled, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err != nil {
		return OIDCIdentity{}, err
	}
	if err := replaceOIDCIdentityRoles(ctx, tx, identityID, roles); err != nil {
		return OIDCIdentity{}, err
	}
	out.Email, out.DisplayName, out.Roles = email, displayName, roles
	if err := appendAudit(ctx, tx, AuditInput{
		TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"),
		Action: "oidc_identity.created", ResourceType: "oidc_identity", ResourceID: identityID,
		RequestID: in.RequestID, TraceID: in.TraceID, After: out,
		Metadata: map[string]any{"provider_id": in.ProviderID, "subject_hash": fmt.Sprintf("%x", hashBytes([]byte(subject)))},
	}); err != nil {
		return OIDCIdentity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OIDCIdentity{}, err
	}
	return out, nil
}

func (s *Store) ListOIDCIdentities(ctx context.Context, tenantID, providerID string) ([]OIDCIdentity, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT i.id,i.tenant_id,i.provider_id,i.user_id,i.subject,u.email,u.display_name,i.enabled,
		       COALESCE(array_agg(r.role ORDER BY r.role) FILTER (WHERE r.role IS NOT NULL),'{}'::text[]),
		       i.created_at,i.updated_at,i.version
		FROM oidc_identities i
		JOIN users u ON u.id=i.user_id
		LEFT JOIN oidc_identity_roles r ON r.identity_id=i.id
		WHERE i.tenant_id=$1 AND i.provider_id=$2
		GROUP BY i.id,u.id ORDER BY i.created_at DESC,i.id DESC`, tenantID, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OIDCIdentity{}
	for rows.Next() {
		identity, err := scanOIDCIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, identity)
	}
	return out, rows.Err()
}

func (s *Store) UpdateOIDCIdentity(ctx context.Context, in UpdateOIDCIdentityInput) (OIDCIdentity, error) {
	if in.ExpectedVersion < 1 {
		return OIDCIdentity{}, fmt.Errorf("expected version is required")
	}
	roles, err := auth.NormalizeRoles(in.Roles)
	if err != nil {
		return OIDCIdentity{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OIDCIdentity{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	before, err := getOIDCIdentityForUpdate(ctx, tx, in.TenantID, in.ID)
	if err != nil {
		return OIDCIdentity{}, err
	}
	if before.Version != in.ExpectedVersion {
		return OIDCIdentity{}, fmt.Errorf("version conflict: current=%d", before.Version)
	}
	var out OIDCIdentity
	err = tx.QueryRow(ctx, `
		UPDATE oidc_identities SET enabled=$3,updated_at=now(),version=version+1
		WHERE tenant_id=$1 AND id=$2 AND version=$4
		RETURNING id,tenant_id,provider_id,user_id,subject,enabled,created_at,updated_at,version`,
		in.TenantID, in.ID, in.Enabled, in.ExpectedVersion,
	).Scan(&out.ID, &out.TenantID, &out.ProviderID, &out.UserID, &out.Subject, &out.Enabled, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err != nil {
		return OIDCIdentity{}, err
	}
	if err := replaceOIDCIdentityRoles(ctx, tx, in.ID, roles); err != nil {
		return OIDCIdentity{}, err
	}
	out.Email, out.DisplayName, out.Roles = before.Email, before.DisplayName, roles
	if err := appendAudit(ctx, tx, AuditInput{
		TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"),
		Action: "oidc_identity.updated", ResourceType: "oidc_identity", ResourceID: in.ID,
		RequestID: in.RequestID, TraceID: in.TraceID, Before: before, After: out,
	}); err != nil {
		return OIDCIdentity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OIDCIdentity{}, err
	}
	return out, nil
}

func (s *Store) AuthenticateOIDCIdentity(ctx context.Context, providerID, subject string) (auth.Principal, error) {
	var tenantID, identityID, userID string
	err := s.pool.QueryRow(ctx, `
		SELECT i.tenant_id,i.id,i.user_id
		FROM oidc_identities i
		JOIN oidc_providers p ON p.id=i.provider_id
		WHERE i.provider_id=$1 AND i.subject=$2 AND i.enabled=true AND p.enabled=true`, providerID, subject,
	).Scan(&tenantID, &identityID, &userID)
	if err == pgx.ErrNoRows {
		return auth.Principal{}, errNoRows
	}
	if err != nil {
		return auth.Principal{}, err
	}
	roles, err := readOIDCIdentityRoles(ctx, s.pool, identityID)
	if err != nil {
		return auth.Principal{}, err
	}
	if len(roles) == 0 {
		return auth.Principal{}, errNoRows
	}
	return auth.Principal{TenantID: tenantID, ActorType: "oidc-user", ActorID: userID, Roles: roles}, nil
}

func getOIDCIdentityForUpdate(ctx context.Context, tx pgx.Tx, tenantID, identityID string) (OIDCIdentity, error) {
	var out OIDCIdentity
	err := tx.QueryRow(ctx, `
		SELECT i.id,i.tenant_id,i.provider_id,i.user_id,i.subject,u.email,u.display_name,i.enabled,i.created_at,i.updated_at,i.version
		FROM oidc_identities i JOIN users u ON u.id=i.user_id
		WHERE i.tenant_id=$1 AND i.id=$2 FOR UPDATE OF i`, tenantID, identityID,
	).Scan(&out.ID, &out.TenantID, &out.ProviderID, &out.UserID, &out.Subject, &out.Email, &out.DisplayName, &out.Enabled, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err == pgx.ErrNoRows {
		return OIDCIdentity{}, errNoRows
	}
	if err != nil {
		return OIDCIdentity{}, err
	}
	out.Roles, err = readOIDCIdentityRoles(ctx, tx, identityID)
	return out, err
}

func replaceOIDCIdentityRoles(ctx context.Context, tx pgx.Tx, identityID string, roles []auth.Role) error {
	if _, err := tx.Exec(ctx, `DELETE FROM oidc_identity_roles WHERE identity_id=$1`, identityID); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err := tx.Exec(ctx, `INSERT INTO oidc_identity_roles(identity_id,role) VALUES($1,$2)`, identityID, role); err != nil {
			return err
		}
	}
	return nil
}

func readOIDCIdentityRoles(ctx context.Context, q queryer, identityID string) ([]auth.Role, error) {
	rows, err := q.Query(ctx, `SELECT role FROM oidc_identity_roles WHERE identity_id=$1 ORDER BY role`, identityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []auth.Role{}
	for rows.Next() {
		var role auth.Role
		if err := rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}

func scanOIDCProvider(row executionStepScanner) (OIDCProvider, error) {
	var out OIDCProvider
	err := row.Scan(&out.ID, &out.TenantID, &out.Name, &out.Issuer, &out.Audience, &out.JWKSURI, &out.SupportedSigningAlgs, &out.Enabled, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	return out, err
}

func scanOIDCIdentity(row executionStepScanner) (OIDCIdentity, error) {
	var out OIDCIdentity
	var roleStrings []string
	err := row.Scan(&out.ID, &out.TenantID, &out.ProviderID, &out.UserID, &out.Subject, &out.Email, &out.DisplayName, &out.Enabled, &roleStrings, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	out.Roles = stringsToRoles(roleStrings)
	return out, err
}

func normalizeSigningAlgs(values []string) []string {
	allowed := map[string]struct{}{"RS256": {}, "RS384": {}, "RS512": {}, "PS256": {}, "PS384": {}, "PS512": {}, "ES256": {}, "ES384": {}, "ES512": {}}
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if _, ok := allowed[value]; !ok {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func validateHTTPSURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("must be an absolute HTTPS URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must not contain userinfo, query, or fragment")
	}
	return nil
}

func hostOnly(raw string) string {
	parsed, _ := url.Parse(raw)
	return parsed.Hostname()
}

func hashBytes(value []byte) []byte {
	hash, _ := hashJSON(string(value))
	return hash
}
