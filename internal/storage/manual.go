package storage

import (
	"context"
	"encoding/json"

	"github.com/DizzyZ7/StormRelay/internal/id"
)

type NotificationChannel struct {
	ID         string          `json:"id"`
	ChannelKey string          `json:"channel_key"`
	Kind       string          `json:"kind"`
	Config     json.RawMessage `json:"config"`
	Enabled    bool            `json:"enabled"`
}

func (s *Store) CreateNotificationChannel(ctx context.Context, tenantID, key, kind string, config json.RawMessage) (NotificationChannel, error) {
	if len(config) == 0 {
		config = json.RawMessage(`{}`)
	}
	channelID, err := id.New()
	if err != nil {
		return NotificationChannel{}, err
	}
	var out NotificationChannel
	err = s.pool.QueryRow(ctx, `INSERT INTO notification_channels(id,tenant_id,channel_key,kind,config) VALUES($1,$2,$3,$4,$5) RETURNING id,channel_key,kind,config,enabled`, channelID, tenantID, key, kind, config).Scan(&out.ID, &out.ChannelKey, &out.Kind, &out.Config, &out.Enabled)
	return out, err
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
