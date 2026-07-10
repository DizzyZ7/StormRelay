package storage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/DizzyZ7/StormRelay/internal/id"
	pluginprotocol "github.com/DizzyZ7/StormRelay/internal/plugins"
	"github.com/jackc/pgx/v5"
)

type RegisterPluginInput struct {
	TenantID, PluginKey, Endpoint, AuthMode, BearerToken, ActorID, RequestID, TraceID string
	TimeoutSeconds                                                                    int
	RetryPolicy                                                                       json.RawMessage
	Manifest                                                                          pluginprotocol.Manifest
}

func (s *Store) RegisterPlugin(ctx context.Context, in RegisterPluginInput) (Plugin, error) {
	if err := pluginprotocol.ValidateManifest(in.Manifest); err != nil {
		return Plugin{}, err
	}
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = 30
	}
	if in.TimeoutSeconds < 1 || in.TimeoutSeconds > 300 {
		return Plugin{}, fmt.Errorf("plugin timeout must be between 1 and 300 seconds")
	}
	if in.AuthMode == "" {
		in.AuthMode = "none"
	}
	if in.AuthMode != "none" && in.AuthMode != "bearer" {
		return Plugin{}, fmt.Errorf("plugin auth_mode must be none or bearer")
	}
	if in.AuthMode == "bearer" && in.BearerToken == "" {
		return Plugin{}, fmt.Errorf("bearer_token is required")
	}
	if in.AuthMode == "none" && in.BearerToken != "" {
		return Plugin{}, fmt.Errorf("bearer_token must be empty when auth_mode is none")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Plugin{}, err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, in.TenantID+"|plugin|"+in.PluginKey); err != nil {
		return Plugin{}, err
	}
	pluginID := ""
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT id,version FROM plugins WHERE tenant_id=$1 AND plugin_key=$2 FOR UPDATE`, in.TenantID, in.PluginKey).Scan(&pluginID, &currentVersion)
	if err == pgx.ErrNoRows {
		pluginID, err = id.New()
		if err != nil {
			return Plugin{}, err
		}
		currentVersion = 0
	} else if err != nil {
		return Plugin{}, err
	}
	var encrypted, bearerHash []byte
	if in.AuthMode == "bearer" {
		encrypted, err = s.box.Encrypt([]byte(in.BearerToken), []byte(pluginID))
		if err != nil {
			return Plugin{}, err
		}
		hash := sha256.Sum256([]byte(in.BearerToken))
		bearerHash = hash[:]
	}
	actions, err := json.Marshal(in.Manifest.Actions)
	if err != nil {
		return Plugin{}, err
	}
	permissionsJSON, err := json.Marshal(collectPluginPermissions(in.Manifest))
	if err != nil {
		return Plugin{}, err
	}
	if len(in.RetryPolicy) == 0 {
		in.RetryPolicy = json.RawMessage(`{"max_attempts":3,"initial_backoff_seconds":1,"max_backoff_seconds":30}`)
	}
	if !json.Valid(in.RetryPolicy) {
		return Plugin{}, fmt.Errorf("retry_policy must be valid JSON")
	}
	if currentVersion == 0 {
		_, err = tx.Exec(ctx, `INSERT INTO plugins(id,tenant_id,plugin_key,endpoint,enabled,active_version,permissions,timeout_seconds,retry_policy,auth_mode,encrypted_secret,bearer_hash,manifest_hash,last_seen_at) VALUES($1,$2,$3,$4,true,$5,$6,$7,$8,$9,$10,$11,$12,now())`, pluginID, in.TenantID, in.PluginKey, in.Endpoint, in.Manifest.Version, permissionsJSON, in.TimeoutSeconds, in.RetryPolicy, in.AuthMode, encrypted, bearerHash, manifestHash(in.Manifest))
	} else {
		_, err = tx.Exec(ctx, `UPDATE plugins SET endpoint=$2,active_version=$3,permissions=$4,timeout_seconds=$5,retry_policy=$6,auth_mode=$7,encrypted_secret=$8,bearer_hash=$9,manifest_hash=$10,last_seen_at=now(),updated_at=now(),version=version+1 WHERE id=$1`, pluginID, in.Endpoint, in.Manifest.Version, permissionsJSON, in.TimeoutSeconds, in.RetryPolicy, in.AuthMode, encrypted, bearerHash, manifestHash(in.Manifest))
	}
	if err != nil {
		return Plugin{}, err
	}
	versionID, err := id.New()
	if err != nil {
		return Plugin{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO plugin_versions(id,plugin_id,version,protocol_version,actions) VALUES($1,$2,$3,$4,$5) ON CONFLICT (plugin_id,version) DO UPDATE SET protocol_version=EXCLUDED.protocol_version,actions=EXCLUDED.actions,discovered_at=now()`, versionID, pluginID, in.Manifest.Version, in.Manifest.ProtocolVersion, actions)
	if err != nil {
		return Plugin{}, err
	}
	if err := appendAudit(ctx, tx, AuditInput{TenantID: in.TenantID, ActorType: "api-key", ActorID: defaultText(in.ActorID, "unknown"), Action: "plugin.registered", ResourceType: "plugin", ResourceID: pluginID, RequestID: in.RequestID, TraceID: in.TraceID, Metadata: map[string]any{"plugin_key": in.PluginKey, "plugin_version": in.Manifest.Version, "protocol_version": in.Manifest.ProtocolVersion, "actions": actionNames(in.Manifest)}}); err != nil {
		return Plugin{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Plugin{}, err
	}
	return s.GetPlugin(ctx, in.TenantID, in.PluginKey)
}

const pluginSelectFields = `SELECT p.id,p.tenant_id,p.plugin_key,p.endpoint,p.enabled,p.active_version,pv.protocol_version,p.permissions,p.timeout_seconds,p.retry_policy,pv.actions,p.auth_mode,p.created_at,p.updated_at,p.last_seen_at,p.version`
const pluginFrom = ` FROM plugins p JOIN plugin_versions pv ON pv.plugin_id=p.id AND pv.version=p.active_version`
const pluginSelect = pluginSelectFields + pluginFrom

func (s *Store) GetPlugin(ctx context.Context, tenantID, pluginKey string) (Plugin, error) {
	var p Plugin
	err := s.pool.QueryRow(ctx, pluginSelect+` WHERE p.tenant_id=$1 AND p.plugin_key=$2`, tenantID, pluginKey).Scan(&p.ID, &p.TenantID, &p.PluginKey, &p.Endpoint, &p.Enabled, &p.ActiveVersion, &p.ProtocolVersion, &p.Permissions, &p.TimeoutSeconds, &p.RetryPolicy, &p.Actions, &p.AuthMode, &p.CreatedAt, &p.UpdatedAt, &p.LastSeenAt, &p.Version)
	if err == pgx.ErrNoRows {
		return Plugin{}, errNoRows
	}
	return p, err
}

func (s *Store) ListPlugins(ctx context.Context, tenantID string) ([]Plugin, error) {
	rows, err := s.pool.Query(ctx, pluginSelect+` WHERE p.tenant_id=$1 ORDER BY p.plugin_key`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plugin{}
	for rows.Next() {
		var p Plugin
		if err := rows.Scan(&p.ID, &p.TenantID, &p.PluginKey, &p.Endpoint, &p.Enabled, &p.ActiveVersion, &p.ProtocolVersion, &p.Permissions, &p.TimeoutSeconds, &p.RetryPolicy, &p.Actions, &p.AuthMode, &p.CreatedAt, &p.UpdatedAt, &p.LastSeenAt, &p.Version); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) PluginCredentialForAction(ctx context.Context, tenantID, pluginKey, action string) (Plugin, string, error) {
	var p Plugin
	var encrypted []byte
	err := s.pool.QueryRow(ctx, pluginSelectFields+`,p.encrypted_secret`+pluginFrom+` WHERE p.tenant_id=$1 AND p.plugin_key=$2 AND p.enabled=true`, tenantID, pluginKey).Scan(&p.ID, &p.TenantID, &p.PluginKey, &p.Endpoint, &p.Enabled, &p.ActiveVersion, &p.ProtocolVersion, &p.Permissions, &p.TimeoutSeconds, &p.RetryPolicy, &p.Actions, &p.AuthMode, &p.CreatedAt, &p.UpdatedAt, &p.LastSeenAt, &p.Version, &encrypted)
	if err == pgx.ErrNoRows {
		return Plugin{}, "", errNoRows
	}
	if err != nil {
		return Plugin{}, "", err
	}
	var actions []pluginprotocol.Action
	if err := json.Unmarshal(p.Actions, &actions); err != nil {
		return Plugin{}, "", err
	}
	if !pluginprotocol.ManifestHasAction(pluginprotocol.Manifest{Actions: actions}, action) {
		return Plugin{}, "", fmt.Errorf("plugin action %q is not declared", action)
	}
	bearer := ""
	if len(encrypted) > 0 {
		plain, err := s.box.Decrypt(encrypted, []byte(p.ID))
		if err != nil {
			return Plugin{}, "", err
		}
		bearer = string(plain)
	}
	return p, bearer, nil
}

func collectPluginPermissions(m pluginprotocol.Manifest) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, a := range m.Actions {
		for _, permission := range a.Permissions {
			if permission != "" && !seen[permission] {
				seen[permission] = true
				out = append(out, permission)
			}
		}
	}
	return out
}
func actionNames(m pluginprotocol.Manifest) []string {
	out := make([]string, 0, len(m.Actions))
	for _, a := range m.Actions {
		out = append(out, a.Name)
	}
	return out
}
func manifestHash(m pluginprotocol.Manifest) []byte {
	b, _ := json.Marshal(m)
	h := sha256.Sum256(b)
	return h[:]
}
