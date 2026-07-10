package storage

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/runbooks"
	"github.com/jackc/pgx/v5"
)

type ApplyRunbookInput struct {
	TenantID, DocumentYAML, ActorID, RequestID, TraceID string
}

type StartExecutionInput struct {
	TenantID, RunbookKey, IncidentID, RequestedBy, RequestID, TraceID string
	DryRun                                                            bool
	Parameters                                                        map[string]any
}

func (s *Store) ApplyRunbook(ctx context.Context, in ApplyRunbookInput) (Runbook, error) {
	doc, err := runbooks.Parse([]byte(in.DocumentYAML))
	if err != nil {
		return Runbook{}, err
	}
	hash := sha256.Sum256([]byte(in.DocumentYAML))
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Runbook{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.TenantID+"|runbook|"+doc.Metadata.ID); err != nil {
		return Runbook{}, err
	}
	var out Runbook
	err = tx.QueryRow(ctx, `SELECT id,tenant_id,runbook_key,enabled,active_version,COALESCE(description,''),created_at,updated_at,version FROM runbooks WHERE tenant_id=$1 AND runbook_key=$2 FOR UPDATE`, in.TenantID, doc.Metadata.ID).Scan(&out.ID, &out.TenantID, &out.RunbookKey, &out.Enabled, &out.ActiveVersion, &out.Description, &out.CreatedAt, &out.UpdatedAt, &out.Version)
	if err == pgx.ErrNoRows {
		if doc.Metadata.Version != 1 {
			return Runbook{}, fmt.Errorf("first runbook version must be 1")
		}
		out.ID, err = id.New()
		if err != nil {
			return Runbook{}, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO runbooks(id,tenant_id,runbook_key,enabled,active_version,description) VALUES($1,$2,$3,true,1,NULLIF($4,'')) RETURNING id,tenant_id,runbook_key,enabled,active_version,COALESCE(description,''),created_at,updated_at,version`, out.ID, in.TenantID, doc.Metadata.ID, doc.Metadata.Description).Scan(&out.ID, &out.TenantID, &out.RunbookKey, &out.Enabled, &out.ActiveVersion, &out.Description, &out.CreatedAt, &out.UpdatedAt, &out.Version)
		if err != nil {
			return Runbook{}, err
		}
	} else if err != nil {
		return Runbook{}, err
	} else if doc.Metadata.Version != out.ActiveVersion+1 {
		return Runbook{}, fmt.Errorf("runbook version must be %d", out.ActiveVersion+1)
	}
	versionID, err := id.New()
	if err != nil {
		return Runbook{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO runbook_versions(id,runbook_id,version,document_yaml,document_hash,created_by) VALUES($1,$2,$3,$4,$5,$6)`, versionID, out.ID, doc.Metadata.Version, in.DocumentYAML, hash[:], defaultText(in.ActorID, "unknown"))
	if err != nil {
		return Runbook{}, err
	}
	if doc.Metadata.Version > 1 {
		err = tx.QueryRow(ctx, `UPDATE runbooks SET active_version=$2,description=NULLIF($3,''),updated_at=now(),version=version+1 WHERE id=$1 RETURNING id,tenant_id,runbook_key,enabled,active_version,COALESCE(description,''),created_at,updated_at,version`, out.ID, doc.Metadata.Version, doc.Metadata.Description).Scan(&out.ID, &out.TenantID, &out.RunbookKey, &out.Enabled, &out.ActiveVersion, &out.Description, &out.CreatedAt, &out.UpdatedAt, &out.Version)
		if err != nil {
			return Runbook{}, err
		}
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"), Action: "runbook.applied", ResourceType: "runbook", ResourceID: out.ID, RequestID: in.RequestID, TraceID: in.TraceID, After: out, Metadata: map[string]any{"runbook_key": out.RunbookKey, "runbook_version": doc.Metadata.Version, "document_sha256": fmt.Sprintf("%x", hash)}}); err != nil {
		return Runbook{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Runbook{}, err
	}
	return out, nil
}

func (s *Store) ListRunbooks(ctx context.Context, tenantID string) ([]Runbook, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,runbook_key,enabled,active_version,COALESCE(description,''),created_at,updated_at,version FROM runbooks WHERE tenant_id=$1 ORDER BY runbook_key`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Runbook{}
	for rows.Next() {
		var item Runbook
		if err := rows.Scan(&item.ID, &item.TenantID, &item.RunbookKey, &item.Enabled, &item.ActiveVersion, &item.Description, &item.CreatedAt, &item.UpdatedAt, &item.Version); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
