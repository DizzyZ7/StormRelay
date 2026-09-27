//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DizzyZ7/StormRelay/internal/api"
	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

// An audit trigger simulates a PostgreSQL audit-write outage after the
// resource INSERT has already executed inside the same transaction.
func TestResourceCreationAndAuditCommitAtomically(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	conn, err := pgx.Connect(ctx, databaseURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	tenant, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, "INSERT INTO tenants(id,slug,name) VALUES($1,$2,$3)", tenant, "atomic-"+tenant, "Atomic creation regression tenant"); err != nil {
		t.Fatal(err)
	}
	account, err := s.CreateServiceAccount(ctx, storage.CreateServiceAccountInput{
		TenantID: tenant,
		Name:     "atomic-create-admin",
		Roles:    []auth.Role{auth.RoleTenantAdmin},
		ActorID:  "integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := s.CreateServiceAccountKey(ctx, storage.CreateServiceAccountKeyInput{
		TenantID:         tenant,
		ServiceAccountID: account.ID,
		ActorID:          "integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := api.New(config.Config{
		DefaultTenantID: tenant,
		BootstrapAPIKey: "atomic-creation-bootstrap",
	}, s, nil, &telemetry.Metrics{}, slog.Default()).Handler()
	post := func(route string, fields map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		body, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, route, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+key.Credential)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	sourceResponse := post("/api/v1/sources", map[string]any{
		"name": "atomic-source-" + tenant, "kind": "generic", "auth_mode": "hmac-sha256",
	})
	if sourceResponse.Code != http.StatusCreated {
		t.Fatalf("successful source response=%d: %s", sourceResponse.Code, sourceResponse.Body.String())
	}
	var sourceResult storage.CreateSourceResult
	if err := json.Unmarshal(sourceResponse.Body.Bytes(), &sourceResult); err != nil {
		t.Fatal(err)
	}
	if sourceResult.Credential == "" || sourceResult.Source.TenantID != tenant {
		t.Fatal("missing credential or incorrect tenant after committed source creation")
	}
	loaded, err := s.GetSourceCredentialsForTenant(ctx, tenant, sourceResult.Source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(loaded.HMACSecret) != sourceResult.Credential {
		t.Fatal("committed source credential does not decrypt")
	}

	privateSecret := "private-channel-secret-" + tenant
	channelResponse := post("/api/v1/notification-channels", map[string]any{
		"key": "atomic-channel-" + tenant, "kind": "telegram",
		"config": map[string]any{"chat_id": "12345", "api_token": privateSecret},
	})
	if channelResponse.Code != http.StatusCreated {
		t.Fatalf("successful channel response=%d: %s", channelResponse.Code, channelResponse.Body.String())
	}
	var channel storage.NotificationChannel
	if err := json.Unmarshal(channelResponse.Body.Bytes(), &channel); err != nil {
		t.Fatal(err)
	}
	if channel.ID == "" {
		t.Fatal("committed notification channel has no ID")
	}

	for _, resource := range []struct {
		action string
		id     string
	}{
		{action: "source.created", id: sourceResult.Source.ID},
		{action: "notification_channel.created", id: channel.ID},
	} {
		var count int
		var actorType, actorID, metadata, afterHash string
		err := conn.QueryRow(ctx, "SELECT COUNT(*), MIN(actor_type), MIN(actor_id), MIN(metadata::text), MIN(encode(after_hash,'hex')) FROM audit_entries WHERE tenant_id=$1 AND action=$2 AND resource_id=$3",
			tenant, resource.action, resource.id).Scan(&count, &actorType, &actorID, &metadata, &afterHash)
		if err != nil {
			t.Fatal(err)
		}
		if count != 1 || actorType != "service-account" || actorID != account.ID {
			t.Fatalf("%s missing or misattributed audit: count=%d actor=%s:%s", resource.action, count, actorType, actorID)
		}
		if len(afterHash) != 64 {
			t.Fatalf("%s has no SHA-256 after hash: %q", resource.action, afterHash)
		}
		if strings.Contains(metadata, sourceResult.Credential) || strings.Contains(metadata, privateSecret) {
			t.Fatalf("%s audit metadata leaked a resource secret", resource.action)
		}
	}

	// The trigger affects only this test's tenant, never unrelated tenants.
	suffix := strings.ReplaceAll(tenant, "-", "")
	functionName := "reject_atomic_creation_audit_" + suffix
	triggerName := "reject_atomic_creation_audit_" + suffix
	createFunction := fmt.Sprintf("CREATE FUNCTION %s() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'forced atomic audit failure'; END; $$ LANGUAGE plpgsql", functionName)
	if _, err := conn.Exec(ctx, createFunction); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(context.Background(), "DROP FUNCTION IF EXISTS "+functionName+"()"); err != nil {
			t.Errorf("cleanup audit function: %v", err)
		}
	})
	createTrigger := fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON audit_entries FOR EACH ROW WHEN (NEW.tenant_id = '%s'::uuid AND NEW.action IN ('source.created','notification_channel.created')) EXECUTE FUNCTION %s()", triggerName, tenant, functionName)
	if _, err := conn.Exec(ctx, createTrigger); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := conn.Exec(context.Background(), "DROP TRIGGER IF EXISTS "+triggerName+" ON audit_entries"); err != nil {
			t.Errorf("cleanup audit trigger: %v", err)
		}
	})
	sourceFailureName := "audit-fail-source-" + tenant
	assertFailedCreation(t, post("/api/v1/sources", map[string]any{
		"name": sourceFailureName, "kind": "generic", "auth_mode": "bearer",
	}), "source_creation_failed")
	channelFailureKey := "audit-fail-channel-" + tenant
	assertFailedCreation(t, post("/api/v1/notification-channels", map[string]any{
		"key": channelFailureKey, "kind": "mock", "config": map[string]any{},
	}), "channel_creation_failed")
	var sources, channels int
	if err := conn.QueryRow(ctx, "SELECT COUNT(*) FROM event_sources WHERE tenant_id=$1 AND name=$2", tenant, sourceFailureName).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, "SELECT COUNT(*) FROM notification_channels WHERE tenant_id=$1 AND channel_key=$2", tenant, channelFailureKey).Scan(&channels); err != nil {
		t.Fatal(err)
	}
	if sources != 0 || channels != 0 {
		t.Fatalf("resources survived failed audit transaction: sources=%d channels=%d", sources, channels)
	}
}

func assertFailedCreation(t *testing.T, response *httptest.ResponseRecorder, code string) {
	t.Helper()
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("audit failure HTTP status=%d, want 500: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(bytes.NewReader(response.Body.Bytes())).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != code {
		t.Fatalf("audit failure code=%q, want %q", envelope.Error.Code, code)
	}
	if strings.Contains(response.Body.String(), "credential") {
		t.Fatalf("failed resource transaction exposed a credential: %s", response.Body.String())
	}
}
