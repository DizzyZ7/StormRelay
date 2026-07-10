package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/jackc/pgx/v5"
)

type ServiceAccount struct {
	ID        string      `json:"id"`
	TenantID  string      `json:"tenant_id"`
	Name      string      `json:"name"`
	Enabled   bool        `json:"enabled"`
	Roles     []auth.Role `json:"roles"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`
	Version   int64       `json:"version"`
}

type ServiceAccountAPIKey struct {
	ID               string     `json:"id"`
	TenantID         string     `json:"tenant_id"`
	ServiceAccountID string     `json:"service_account_id"`
	KeyPrefix        string     `json:"key_prefix"`
	ExpiresAt        *time.Time `json:"expires_at,omitempty"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	LastUsedAt       *time.Time `json:"last_used_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

type CreatedServiceAccountKey struct {
	Key        ServiceAccountAPIKey `json:"key"`
	Credential string               `json:"credential"`
}

type CreateServiceAccountInput struct {
	TenantID, Name, ActorID, RequestID, TraceID string
	Roles                                       []auth.Role
}

type UpdateServiceAccountInput struct {
	TenantID, ID, Name, ActorID, RequestID, TraceID string
	Enabled                                         bool
	Roles                                           []auth.Role
	ExpectedVersion                                 int64
}

type CreateServiceAccountKeyInput struct {
	TenantID, ServiceAccountID, ActorID, RequestID, TraceID string
	ExpiresAt                                               *time.Time
}

func (s *Store) CreateServiceAccount(ctx context.Context, in CreateServiceAccountInput) (ServiceAccount, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 200 {
		return ServiceAccount{}, fmt.Errorf("service account name must contain 1 to 200 bytes")
	}
	roles, err := auth.NormalizeRoles(in.Roles)
	if err != nil {
		return ServiceAccount{}, err
	}
	accountID, err := id.New()
	if err != nil {
		return ServiceAccount{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ServiceAccount{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var out ServiceAccount
	err = tx.QueryRow(ctx, `
		INSERT INTO service_accounts(id, tenant_id, name)
		VALUES($1, $2, $3)
		RETURNING id, tenant_id, name, enabled, created_at, updated_at, version`,
		accountID, in.TenantID, name,
	).Scan(&out.ID, &out.TenantID, &out.Name, &out.Enabled, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err != nil {
		return ServiceAccount{}, err
	}
	if err := replaceServiceAccountRoles(ctx, tx, accountID, roles); err != nil {
		return ServiceAccount{}, err
	}
	out.Roles = roles
	if err := appendAudit(ctx, tx, AuditInput{
		TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"),
		Action: "service_account.created", ResourceType: "service_account", ResourceID: accountID,
		RequestID: in.RequestID, TraceID: in.TraceID, After: out,
		Metadata: map[string]any{"roles": roles},
	}); err != nil {
		return ServiceAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceAccount{}, err
	}
	return out, nil
}

func (s *Store) ListServiceAccounts(ctx context.Context, tenantID string) ([]ServiceAccount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT sa.id, sa.tenant_id, sa.name, sa.enabled, sa.created_at, sa.updated_at, sa.version,
		       COALESCE(array_agg(sar.role ORDER BY sar.role) FILTER (WHERE sar.role IS NOT NULL), '{}'::text[])
		FROM service_accounts sa
		LEFT JOIN service_account_roles sar ON sar.service_account_id = sa.id
		WHERE sa.tenant_id = $1
		GROUP BY sa.id
		ORDER BY sa.created_at DESC, sa.id DESC`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ServiceAccount{}
	for rows.Next() {
		account, err := scanServiceAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, account)
	}
	return out, rows.Err()
}

func (s *Store) GetServiceAccount(ctx context.Context, tenantID, accountID string) (ServiceAccount, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT sa.id, sa.tenant_id, sa.name, sa.enabled, sa.created_at, sa.updated_at, sa.version,
		       COALESCE(array_agg(sar.role ORDER BY sar.role) FILTER (WHERE sar.role IS NOT NULL), '{}'::text[])
		FROM service_accounts sa
		LEFT JOIN service_account_roles sar ON sar.service_account_id = sa.id
		WHERE sa.tenant_id = $1 AND sa.id = $2
		GROUP BY sa.id`, tenantID, accountID)
	account, err := scanServiceAccount(row)
	if err == pgx.ErrNoRows {
		return ServiceAccount{}, errNoRows
	}
	return account, err
}

func (s *Store) UpdateServiceAccount(ctx context.Context, in UpdateServiceAccountInput) (ServiceAccount, error) {
	if in.ExpectedVersion < 1 {
		return ServiceAccount{}, fmt.Errorf("expected version is required")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 200 {
		return ServiceAccount{}, fmt.Errorf("service account name must contain 1 to 200 bytes")
	}
	roles, err := auth.NormalizeRoles(in.Roles)
	if err != nil {
		return ServiceAccount{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ServiceAccount{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var before ServiceAccount
	err = tx.QueryRow(ctx, `
		SELECT id, tenant_id, name, enabled, created_at, updated_at, version
		FROM service_accounts
		WHERE tenant_id = $1 AND id = $2
		FOR UPDATE`, in.TenantID, in.ID,
	).Scan(&before.ID, &before.TenantID, &before.Name, &before.Enabled, &before.CreatedAt, &before.UpdatedAt, &before.Version)
	if err == pgx.ErrNoRows {
		return ServiceAccount{}, errNoRows
	}
	if err != nil {
		return ServiceAccount{}, err
	}
	before.Roles, err = readServiceAccountRoles(ctx, tx, in.ID)
	if err != nil {
		return ServiceAccount{}, err
	}
	if before.Version != in.ExpectedVersion {
		return ServiceAccount{}, fmt.Errorf("version conflict: current=%d", before.Version)
	}

	var out ServiceAccount
	err = tx.QueryRow(ctx, `
		UPDATE service_accounts
		SET name = $3, enabled = $4, updated_at = now(), version = version + 1
		WHERE tenant_id = $1 AND id = $2 AND version = $5
		RETURNING id, tenant_id, name, enabled, created_at, updated_at, version`,
		in.TenantID, in.ID, name, in.Enabled, in.ExpectedVersion,
	).Scan(&out.ID, &out.TenantID, &out.Name, &out.Enabled, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err == pgx.ErrNoRows {
		return ServiceAccount{}, fmt.Errorf("version conflict")
	}
	if err != nil {
		return ServiceAccount{}, err
	}
	if err := replaceServiceAccountRoles(ctx, tx, in.ID, roles); err != nil {
		return ServiceAccount{}, err
	}
	out.Roles = roles
	if err := appendAudit(ctx, tx, AuditInput{
		TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"),
		Action: "service_account.updated", ResourceType: "service_account", ResourceID: in.ID,
		RequestID: in.RequestID, TraceID: in.TraceID, Before: before, After: out,
		Metadata: map[string]any{"roles": roles},
	}); err != nil {
		return ServiceAccount{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceAccount{}, err
	}
	return out, nil
}

func (s *Store) CreateServiceAccountKey(ctx context.Context, in CreateServiceAccountKeyInput) (CreatedServiceAccountKey, error) {
	if in.ExpiresAt != nil {
		now := time.Now().UTC()
		if !in.ExpiresAt.After(now) || in.ExpiresAt.After(now.Add(2*365*24*time.Hour)) {
			return CreatedServiceAccountKey{}, fmt.Errorf("key expiry must be in the future and within two years")
		}
	}
	credential, prefix, hash, err := newServiceAccountCredential()
	if err != nil {
		return CreatedServiceAccountKey{}, err
	}
	keyID, err := id.New()
	if err != nil {
		return CreatedServiceAccountKey{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CreatedServiceAccountKey{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var enabled bool
	err = tx.QueryRow(ctx, `
		SELECT enabled FROM service_accounts
		WHERE tenant_id = $1 AND id = $2
		FOR UPDATE`, in.TenantID, in.ServiceAccountID,
	).Scan(&enabled)
	if err == pgx.ErrNoRows {
		return CreatedServiceAccountKey{}, errNoRows
	}
	if err != nil {
		return CreatedServiceAccountKey{}, err
	}
	if !enabled {
		return CreatedServiceAccountKey{}, fmt.Errorf("service account is disabled")
	}

	var key ServiceAccountAPIKey
	err = tx.QueryRow(ctx, `
		INSERT INTO service_account_api_keys(id, tenant_id, service_account_id, key_prefix, key_hash, expires_at)
		VALUES($1, $2, $3, $4, $5, $6)
		RETURNING id, tenant_id, service_account_id, key_prefix, expires_at, revoked_at, last_used_at, created_at`,
		keyID, in.TenantID, in.ServiceAccountID, prefix, hash, in.ExpiresAt,
	).Scan(&key.ID, &key.TenantID, &key.ServiceAccountID, &key.KeyPrefix, &key.ExpiresAt, &key.RevokedAt, &key.LastUsedAt, &key.CreatedAt)
	if err != nil {
		return CreatedServiceAccountKey{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{
		TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"),
		Action: "service_account_key.created", ResourceType: "service_account_key", ResourceID: keyID,
		RequestID: in.RequestID, TraceID: in.TraceID, After: key,
		Metadata: map[string]any{"service_account_id": in.ServiceAccountID, "key_prefix": prefix, "expires_at": in.ExpiresAt},
	}); err != nil {
		return CreatedServiceAccountKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return CreatedServiceAccountKey{}, err
	}
	return CreatedServiceAccountKey{Key: key, Credential: credential}, nil
}

func (s *Store) ListServiceAccountKeys(ctx context.Context, tenantID, accountID string) ([]ServiceAccountAPIKey, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT k.id, k.tenant_id, k.service_account_id, k.key_prefix,
		       k.expires_at, k.revoked_at, k.last_used_at, k.created_at
		FROM service_account_api_keys k
		JOIN service_accounts sa ON sa.id = k.service_account_id
		WHERE k.tenant_id = $1 AND sa.id = $2
		ORDER BY k.created_at DESC, k.id DESC`, tenantID, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ServiceAccountAPIKey{}
	for rows.Next() {
		var key ServiceAccountAPIKey
		if err := rows.Scan(&key.ID, &key.TenantID, &key.ServiceAccountID, &key.KeyPrefix, &key.ExpiresAt, &key.RevokedAt, &key.LastUsedAt, &key.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, key)
	}
	return out, rows.Err()
}

func (s *Store) RevokeServiceAccountKey(ctx context.Context, tenantID, keyID, actorID, requestID, traceID string) (ServiceAccountAPIKey, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ServiceAccountAPIKey{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	var key ServiceAccountAPIKey
	err = tx.QueryRow(ctx, `
		UPDATE service_account_api_keys
		SET revoked_at = COALESCE(revoked_at, now())
		WHERE tenant_id = $1 AND id = $2
		RETURNING id, tenant_id, service_account_id, key_prefix, expires_at, revoked_at, last_used_at, created_at`,
		tenantID, keyID,
	).Scan(&key.ID, &key.TenantID, &key.ServiceAccountID, &key.KeyPrefix, &key.ExpiresAt, &key.RevokedAt, &key.LastUsedAt, &key.CreatedAt)
	if err == pgx.ErrNoRows {
		return ServiceAccountAPIKey{}, errNoRows
	}
	if err != nil {
		return ServiceAccountAPIKey{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{
		TenantID: tenantID, ActorType: "api-key", ActorID: defaultText(actorID, "unknown"),
		Action: "service_account_key.revoked", ResourceType: "service_account_key", ResourceID: keyID,
		RequestID: requestID, TraceID: traceID, After: key,
		Metadata: map[string]any{"service_account_id": key.ServiceAccountID, "key_prefix": key.KeyPrefix},
	}); err != nil {
		return ServiceAccountAPIKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ServiceAccountAPIKey{}, err
	}
	return key, nil
}

func (s *Store) AuthenticateServiceAccountKey(ctx context.Context, credential string) (auth.Principal, error) {
	prefix, ok := serviceAccountKeyPrefix(credential)
	actualHash := sha256.Sum256([]byte(credential))
	if !ok {
		var dummy [32]byte
		_ = subtle.ConstantTimeCompare(actualHash[:], dummy[:])
		return auth.Principal{}, errNoRows
	}

	var keyID, tenantID, accountID string
	var expectedHash []byte
	err := s.pool.QueryRow(ctx, `
		SELECT k.id, k.tenant_id, k.service_account_id, k.key_hash
		FROM service_account_api_keys k
		JOIN service_accounts sa ON sa.id = k.service_account_id
		WHERE k.key_prefix = $1
		  AND k.revoked_at IS NULL
		  AND (k.expires_at IS NULL OR k.expires_at > now())
		  AND sa.enabled = true`, prefix,
	).Scan(&keyID, &tenantID, &accountID, &expectedHash)
	if err != nil {
		var dummy [32]byte
		_ = subtle.ConstantTimeCompare(actualHash[:], dummy[:])
		return auth.Principal{}, errNoRows
	}
	if subtle.ConstantTimeCompare(actualHash[:], expectedHash) != 1 {
		return auth.Principal{}, errNoRows
	}

	roles, err := readServiceAccountRoles(ctx, s.pool, accountID)
	if err != nil {
		return auth.Principal{}, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE service_account_api_keys SET last_used_at = now() WHERE id = $1`, keyID); err != nil {
		return auth.Principal{}, err
	}
	return auth.Principal{
		TenantID: tenantID, ActorType: "service-account", ActorID: accountID,
		Roles: roles, KeyID: keyID,
	}, nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readServiceAccountRoles(ctx context.Context, q queryer, accountID string) ([]auth.Role, error) {
	rows, err := q.Query(ctx, `SELECT role FROM service_account_roles WHERE service_account_id = $1 ORDER BY role`, accountID)
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

func replaceServiceAccountRoles(ctx context.Context, tx pgx.Tx, accountID string, roles []auth.Role) error {
	if _, err := tx.Exec(ctx, `DELETE FROM service_account_roles WHERE service_account_id = $1`, accountID); err != nil {
		return err
	}
	for _, role := range roles {
		if _, err := tx.Exec(ctx, `INSERT INTO service_account_roles(service_account_id, role) VALUES($1, $2)`, accountID, role); err != nil {
			return err
		}
	}
	return nil
}

func scanServiceAccount(row executionStepScanner) (ServiceAccount, error) {
	var account ServiceAccount
	var roles []string
	err := row.Scan(&account.ID, &account.TenantID, &account.Name, &account.Enabled, &account.CreatedAt, &account.UpdatedAt, &account.Version, &roles)
	account.Roles = stringsToRoles(roles)
	return account, err
}

func stringsToRoles(values []string) []auth.Role {
	out := make([]auth.Role, 0, len(values))
	for _, value := range values {
		out = append(out, auth.Role(value))
	}
	return out
}

func newServiceAccountCredential() (credential, prefix string, hash []byte, err error) {
	var prefixBytes [8]byte
	var secretBytes [32]byte
	if _, err = rand.Read(prefixBytes[:]); err != nil {
		return "", "", nil, fmt.Errorf("generate key prefix: %w", err)
	}
	if _, err = rand.Read(secretBytes[:]); err != nil {
		return "", "", nil, fmt.Errorf("generate key secret: %w", err)
	}
	prefix = hex.EncodeToString(prefixBytes[:])
	credential = "srk_" + prefix + "_" + base64.RawURLEncoding.EncodeToString(secretBytes[:])
	sum := sha256.Sum256([]byte(credential))
	return credential, prefix, sum[:], nil
}

func serviceAccountKeyPrefix(credential string) (string, bool) {
	parts := strings.SplitN(credential, "_", 3)
	if len(parts) != 3 || parts[0] != "srk" || len(parts[1]) != 16 {
		return "", false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", false
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secret) != 32 {
		return "", false
	}
	return parts[1], true
}
