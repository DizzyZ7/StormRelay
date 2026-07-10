package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/policies"
	"github.com/jackc/pgx/v5"
)

type ApplyPolicyInput struct{ TenantID, DocumentYAML, ActorID, RequestID, TraceID string }
type AppliedPolicy struct {
	ID        string    `json:"id"`
	PolicyKey string    `json:"policy_key"`
	Version   int       `json:"version"`
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Store) ApplyPolicy(ctx context.Context, in ApplyPolicyInput) (AppliedPolicy, error) {
	doc, err := policies.Parse([]byte(in.DocumentYAML))
	if err != nil {
		return AppliedPolicy{}, err
	}
	hash := sha256.Sum256([]byte(in.DocumentYAML))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppliedPolicy{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.TenantID+"|policy|"+doc.Metadata.ID); err != nil {
		return AppliedPolicy{}, err
	}
	var policyID string
	var currentVersion int
	var enabled bool
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT id,active_version,enabled,updated_at FROM policies WHERE tenant_id=$1 AND policy_key=$2 FOR UPDATE`, in.TenantID, doc.Metadata.ID).Scan(&policyID, &currentVersion, &enabled, &updated)
	if err == pgx.ErrNoRows {
		if doc.Metadata.Version != 1 {
			return AppliedPolicy{}, fmt.Errorf("first policy version must be 1")
		}
		policyID, err = id.New()
		if err != nil {
			return AppliedPolicy{}, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO policies(id,tenant_id,policy_key,enabled,active_version) VALUES($1,$2,$3,true,1)`, policyID, in.TenantID, doc.Metadata.ID)
		if err != nil {
			return AppliedPolicy{}, err
		}
		enabled = true
		currentVersion = 0
	} else if err != nil {
		return AppliedPolicy{}, err
	} else if doc.Metadata.Version != currentVersion+1 {
		return AppliedPolicy{}, fmt.Errorf("policy version must be %d", currentVersion+1)
	}
	versionID, err := id.New()
	if err != nil {
		return AppliedPolicy{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO policy_versions(id,policy_id,version,document_yaml,document_hash,created_by) VALUES($1,$2,$3,$4,$5,$6)`, versionID, policyID, doc.Metadata.Version, in.DocumentYAML, hash[:], defaultText(in.ActorID, "unknown"))
	if err != nil {
		return AppliedPolicy{}, err
	}
	_, err = tx.Exec(ctx, `UPDATE policies SET active_version=$2,updated_at=now(),version=version+1 WHERE id=$1`, policyID, doc.Metadata.Version)
	if err != nil {
		return AppliedPolicy{}, err
	}
	out := AppliedPolicy{ID: policyID, PolicyKey: doc.Metadata.ID, Version: doc.Metadata.Version, Enabled: enabled, UpdatedAt: time.Now().UTC()}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"), Action: "policy.applied", ResourceType: "policy", ResourceID: policyID, RequestID: in.RequestID, TraceID: in.TraceID, After: out, Metadata: map[string]any{"policy_id": doc.Metadata.ID, "policy_version": doc.Metadata.Version, "document_sha256": fmt.Sprintf("%x", hash)}}); err != nil {
		return AppliedPolicy{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AppliedPolicy{}, err
	}
	return out, nil
}
func (s *Store) ListPolicies(ctx context.Context, tenantID string) ([]AppliedPolicy, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,policy_key,active_version,enabled,updated_at FROM policies WHERE tenant_id=$1 ORDER BY policy_key`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppliedPolicy{}
	for rows.Next() {
		var x AppliedPolicy
		if err := rows.Scan(&x.ID, &x.PolicyKey, &x.Version, &x.Enabled, &x.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
