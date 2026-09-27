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
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/jackc/pgx/v5"
)

func TestTenantScopedSourceAPI(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	foreignTenant, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, databaseURL())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "INSERT INTO tenants(id,slug,name) VALUES($1,$2,$3)", foreignTenant, "tenant-"+foreignTenant, "Isolation regression tenant")
	if err != nil {
		t.Fatal(err)
	}

	ownSource, err := store.CreateSource(ctx, storage.CreateSourceInput{
	    TenantID: foreignTenant, Name: "tenant-scope-" + foreignTenant,
	    Kind: "generic", AuthMode: ingestion.AuthHMAC,
	})
	if err != nil {
		t.Fatal(err)
	}
	defaultSource, err := store.CreateSource(ctx, storage.CreateSourceInput{
	    TenantID: tenantID, Name: "default-scope-" + foreignTenant,
	    Kind: "generic", AuthMode: ingestion.AuthHMAC,
	})
	if err != nil {
		t.Fatal(err)
	}

	account, err := store.CreateServiceAccount(ctx, storage.CreateServiceAccountInput{
	    TenantID: foreignTenant, Name: "isolation-admin", Roles: []auth.Role{auth.RoleTenantAdmin},
	    ActorID: "integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateServiceAccountKey(ctx, storage.CreateServiceAccountKeyInput{
	    TenantID: foreignTenant, ServiceAccountID: account.ID, ActorID: "integration",
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := api.New(config.Config{
	    DefaultTenantID: tenantID, BootstrapAPIKey: "isolation-bootstrap-key",
	    AllowUnauthenticatedSources: false,
	}, store, nil, &telemetry.Metrics{}, slog.Default()).Handler()

	request := func(token, method, path, body string) *httptest.ResponseRecorder {
	    t.Helper()
	    req := httptest.NewRequest(method, path, strings.NewReader(body))
	    req.Header.Set("Authorization", "Bearer "+token)
	    req.Header.Set("Content-Type", "application/json")
	    rr := httptest.NewRecorder()
	    handler.ServeHTTP(rr, req)
	    return rr
	}

	listed := request(key.Credential, http.MethodGet, "/api/v1/sources", "")
	if listed.Code != http.StatusOK {
		t.Fatalf("source list status=%d: %s", listed.Code, listed.Body.String())
	}
	var list struct {
		Items []storage.Source `json:"items"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	seenOwn := false
	for _, item := range list.Items {
	    if item.TenantID != foreignTenant || item.ID == defaultSource.Source.ID {
	        t.Fatalf("cross-tenant source visible: %+v", item)
	    }
	    if item.ID == ownSource.Source.ID { seenOwn = true }
	}
	if !seenOwn {
		t.Fatal("own tenant source missing")
	}

	created := request(key.Credential, http.MethodPost, "/api/v1/sources",
	    fmt.Sprintf(`{"name":"created-%s","kind":"generic","auth_mode":"hmac-sha256"}`, foreignTenant))
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d: %s", created.Code, created.Body.String())
	}
	var createdBody struct {
		Source storage.Source `json:"source"`
	}
	if err := json.NewDecoder(bytes.NewReader(created.Body.Bytes())).Decode(&createdBody); err != nil {
		t.Fatal(err)
	}
	if createdBody.Source.TenantID != foreignTenant {
		t.Fatalf("created in wrong tenant: %s", createdBody.Source.TenantID)
	}

	crossTest := request(key.Credential, http.MethodPost, "/api/v1/sources/"+defaultSource.Source.ID+"/test", "")
	if crossTest.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant source test status=%d: %s", crossTest.Code, crossTest.Body.String())
	}

	manual := request(key.Credential, http.MethodPost, "/api/v1/incidents",
	    `{"title":"must not write into bootstrap tenant","severity":"warning"}`)
	if manual.Code != http.StatusNotImplemented {
		t.Fatalf("manual cross-tenant status=%d: %s", manual.Code, manual.Body.String())
	}

	if r := request(key.Credential, http.MethodGet, "/api/v1/incidents", ""); r.Code != http.StatusOK {
	    t.Fatalf("incident list status=%d: %s", r.Code, r.Body.String())
	}
}
