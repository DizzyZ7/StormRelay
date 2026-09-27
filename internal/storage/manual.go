package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DizzyZ7/StormRelay/internal/id"
)

type NotificationChannel struct {
	ID         string          `json:"id"`
	ChannelKey string          `json:"channel_key"`
	Kind       string          `json:"kind"`
	Config     json.RawMessage `json:"config"`
	Enabled    bool            `json:"enabled"`
}

// CreateNotificationChannel preserves the internal, non-HTTP creation API.
func (s *Store) CreateNotificationChannel(ctx context.Context, tenantID, key, kind string, config json.RawMessage) (NotificationChannel, error) {
	return s.createNotificationChannel(ctx, tenantID, key, kind, config, nil)
}

// CreateNotificationChannelWithAudit guarantees both creation and its audit
// record commit together, without storing channel secrets in the audit record.
func (s *Store) CreateNotificationChannelWithAudit(ctx context.Context, tenantID, key, kind string, config json.RawMessage, audit AuditInput) (NotificationChannel, error) {
	if audit.TenantID != tenantID || tenantID == "" || strings.TrimSpace(audit.ActorType) == "" || strings.TrimSpace(audit.ActorID) == "" {
		return NotificationChannel{}, fmt.Errorf("notification audit tenant and actor must match the authenticated request")
	}
	return s.createNotificationChannel(ctx, tenantID, key, kind, config, &audit)
}

func (s *Store) createNotificationChannel(ctx context.Context, tenantID, key, kind string, config json.RawMessage, audit *AuditInput) (NotificationChannel, error) {
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	channelID, err := id.New()
	if err != nil {
		return NotificationChannel{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("begin notification channel creation: %w", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var out NotificationChannel
	err = tx.QueryRow(ctx, `INSERT INTO notification_channels(id,tenant_id,channel_key,kind,config) VALUES($1,$2,$3,$4,$5) RETURNING id,channel_key,kind,config,enabled`, channelID, tenantID, key, kind, config).Scan(&out.ID, &out.ChannelKey, &out.Kind, &out.Config, &out.Enabled)
	if err != nil {
		return NotificationChannel{}, fmt.Errorf("create notification channel: %w", err)
	}
	if audit != nil {
		entry := *audit
		entry.Action = "notification_channel.created"
		entry.ResourceType = "notification_channel"
		entry.ResourceID = out.ID
		entry.Before = nil
		entry.After = map[string]any{"id": out.ID, "key": out.ChannelKey, "kind": out.Kind, "enabled": out.Enabled}
		entry.Metadata = nil
		if err := appendAudit(ctx, tx, entry); err != nil {
			return NotificationChannel{}, fmt.Errorf("audit notification channel creation: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return NotificationChannel{}, fmt.Errorf("commit notification channel creation: %w", err)
	}
	return out, nil
}
func (s *Store) ListNotificationChannels(ctx context.Context, tenantID string) ([]NotificationChannel, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,channel_key,kind,config,enabled FROM notification_channels WHERE tenant_id=$1 ORDER BY channel_key`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NotificationChannel{}
	for rows.Next() {
		var x NotificationChannel
		if err := rows.Scan(&x.ID, &x.ChannelKey, &x.Kind, &x.Config, &x.Enabled); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
