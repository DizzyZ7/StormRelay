package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

func (s *Store) enqueueNotifications(ctx context.Context, tx pgx.Tx, incident Incident, channelKeys []string, baseURL string) ([]string, error) {
	rawToken, err := randomSecret(32)
	if err != nil {
		return nil, err
	}
	tokenHash := cryptox.HashSecret(rawToken)
	tokenID, err := id.New()
	if err != nil {
		return nil, err
	}
	encryptedToken, err := s.box.Encrypt([]byte(rawToken), []byte(incident.ID))
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{
		"incident_id":  incident.ID,
		"title":        incident.Title,
		"severity":     incident.Severity,
		"state":        incident.State,
		"service":      incident.Service,
		"environment":  incident.Environment,
		"ack_base_url": baseURL,
		"traceparent":  telemetry.TraceParentFromContext(ctx),
	})
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for _, key := range channelKeys {
		var channelID, kind string
		err := tx.QueryRow(ctx, `SELECT id,kind FROM notification_channels WHERE tenant_id=$1 AND channel_key=$2 AND enabled=true`, incident.TenantID, key).Scan(&channelID, &kind)
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("notification channel %q does not exist or is disabled", key)
		}
		if err != nil {
			return nil, err
		}
		deliveryID, err := id.New()
		if err != nil {
			return nil, err
		}
		dedupe := fmt.Sprintf("incident:%s:channel:%s:state:%s:severity:%s", incident.ID, channelID, incident.State, incident.Severity)
		command, err := tx.Exec(ctx, `INSERT INTO delivery_attempts(id,tenant_id,incident_id,channel_id,channel_kind,dedupe_key,status,payload,ack_token_ciphertext) VALUES($1,$2,$3,$4,$5,$6,'pending',$7,$8) ON CONFLICT (tenant_id,dedupe_key) DO NOTHING`, deliveryID, incident.TenantID, incident.ID, channelID, kind, dedupe, payload, encryptedToken)
		if err != nil {
			return nil, err
		}
		if command.RowsAffected() > 0 {
			ids = append(ids, deliveryID)
		}
	}
	if len(ids) > 0 {
		_, err = tx.Exec(ctx, `INSERT INTO incident_ack_tokens(id,tenant_id,incident_id,token_hash,expires_at) VALUES($1,$2,$3,$4,$5)`, tokenID, incident.TenantID, incident.ID, tokenHash[:], time.Now().UTC().Add(24*time.Hour))
		if err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func (s *Store) ClaimDeliveries(ctx context.Context, limit int) ([]Delivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `UPDATE delivery_attempts SET status=CASE WHEN channel_kind='mock' THEN 'failed' ELSE 'ambiguous' END,sanitized_error='delivery lease expired; outcome unknown',lease_expires_at=NULL,updated_at=now() WHERE status='delivering' AND lease_expires_at<=now()`)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT d.id,d.tenant_id,d.incident_id,d.channel_id,d.channel_kind,d.dedupe_key,d.payload,c.config,d.attempt,d.ack_token_ciphertext FROM delivery_attempts d LEFT JOIN notification_channels c ON c.id=d.channel_id WHERE status IN ('pending','failed') AND next_attempt_at<=now() ORDER BY d.next_attempt_at FOR UPDATE OF d SKIP LOCKED LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type claimed struct {
		Delivery
		cipher []byte
	}
	items := []claimed{}
	for rows.Next() {
		var x claimed
		if err := rows.Scan(&x.ID, &x.TenantID, &x.IncidentID, &x.ChannelID, &x.Kind, &x.DedupeKey, &x.Payload, &x.Config, &x.Attempt, &x.cipher); err != nil {
			return nil, err
		}
		items = append(items, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Delivery, 0, len(items))
	for _, x := range items {
		_, err = tx.Exec(ctx, `UPDATE delivery_attempts SET status='delivering',attempt=attempt+1,lease_expires_at=now()+interval '30 seconds',updated_at=now() WHERE id=$1`, x.ID)
		if err != nil {
			return nil, err
		}
		plain, decryptErr := s.box.Decrypt(x.cipher, []byte(x.IncidentID))
		if decryptErr != nil {
			return nil, decryptErr
		}
		x.Payload, x.TraceParent, err = prepareDeliveryPayload(x.Payload, string(plain))
		if err != nil {
			return nil, err
		}
		out = append(out, x.Delivery)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) CompleteDelivery(ctx context.Context, deliveryID string, providerRef string, deliveryErr error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var tenantID, incidentID, kind string
	var attempt int
	if err = tx.QueryRow(ctx, `SELECT tenant_id,incident_id,channel_kind,attempt FROM delivery_attempts WHERE id=$1 FOR UPDATE`, deliveryID).Scan(&tenantID, &incidentID, &kind, &attempt); err != nil {
		return err
	}
	action := "notification.delivered"
	metadata := map[string]any{"channel_kind": kind, "attempt": attempt}
	if deliveryErr == nil {
		_, err = tx.Exec(ctx, `UPDATE delivery_attempts SET status='delivered',provider_reference=NULLIF($2,''),delivered_at=now(),lease_expires_at=NULL,updated_at=now(),sanitized_error=NULL WHERE id=$1`, deliveryID, providerRef)
		metadata["provider_reference"] = truncate(providerRef, 128)
	} else {
		message := sanitizeError(deliveryErr.Error())
		_, err = tx.Exec(ctx, `UPDATE delivery_attempts SET status=CASE WHEN attempt>=5 THEN 'ambiguous' ELSE 'failed' END,sanitized_error=$2,lease_expires_at=NULL,next_attempt_at=now()+(LEAST(300,power(2,attempt))::text||' seconds')::interval,updated_at=now() WHERE id=$1`, deliveryID, message)
		action = "notification.failed"
		metadata["error"] = message
	}
	if err != nil {
		return err
	}
	if err = appendAudit(ctx, tx, AuditInput{TenantID: tenantID, ActorType: "service", ActorID: "stormrelay-worker", Action: action, ResourceType: "delivery", ResourceID: deliveryID, Metadata: metadata}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func sanitizeError(value string) string {
	return truncate(value, 500)
}

func truncate(value string, limit int) string {
	if limit > 0 && len(value) > limit {
		return value[:limit]
	}
	return value
}

func prepareDeliveryPayload(payload json.RawMessage, token string) (json.RawMessage, string, error) {
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, "", err
	}
	base, _ := value["ack_base_url"].(string)
	traceParent, _ := value["traceparent"].(string)
	delete(value, "ack_base_url")
	delete(value, "traceparent")
	value["ack_url"] = strings.TrimRight(base, "/") + "/api/v1/ack/" + token
	clean, err := json.Marshal(value)
	return clean, traceParent, err
}

func addAckURL(payload json.RawMessage, token string) (json.RawMessage, error) {
	clean, _, err := prepareDeliveryPayload(payload, token)
	return clean, err
}
