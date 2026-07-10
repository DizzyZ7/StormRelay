package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/jackc/pgx/v5"
)

type AuditInput struct {
	TenantID, ActorType, ActorID, Action, ResourceType, ResourceID, RequestID, TraceID string
	Before                                                                             any
	After                                                                              any
	Metadata                                                                           map[string]any
}

func appendAudit(ctx context.Context, tx pgx.Tx, in AuditInput) error {
	auditID, err := id.New()
	if err != nil {
		return err
	}
	before, err := hashJSON(in.Before)
	if err != nil {
		return err
	}
	after, err := hashJSON(in.After)
	if err != nil {
		return err
	}
	metadata, err := json.Marshal(in.Metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_entries(id,tenant_id,actor_type,actor_id,action,resource_type,resource_id,request_id,trace_id,before_hash,after_hash,metadata) VALUES($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,''),$10,$11,$12)`, auditID, in.TenantID, in.ActorType, in.ActorID, in.Action, in.ResourceType, in.ResourceID, in.RequestID, in.TraceID, nullableHash(before), nullableHash(after), metadata)
	return err
}

func hashJSON(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	return sum[:], nil
}
func nullableHash(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func (s *Store) ExportAuditJSONL(ctx context.Context, tenantID string, after time.Time, w io.Writer) error {
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,actor_type,actor_id,action,resource_type,resource_id,COALESCE(request_id,''),COALESCE(trace_id,''),before_hash,after_hash,metadata,created_at FROM audit_entries WHERE tenant_id=$1 AND created_at >= $2 ORDER BY created_at,id`, tenantID, after)
	if err != nil {
		return err
	}
	defer rows.Close()
	enc := json.NewEncoder(w)
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.TenantID, &e.ActorType, &e.ActorID, &e.Action, &e.ResourceType, &e.ResourceID, &e.RequestID, &e.TraceID, &e.BeforeHash, &e.AfterHash, &e.Metadata, &e.CreatedAt); err != nil {
			return err
		}
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("encode audit record: %w", err)
		}
	}
	return rows.Err()
}

func (s *Store) RecordAudit(ctx context.Context, in AuditInput) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := appendAudit(ctx, tx, in); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
