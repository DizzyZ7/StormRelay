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
	"sync"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/api"
	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/DizzyZ7/StormRelay/internal/messaging"
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
		TenantID: foreignTenant,
		Name:     "tenant-scope-" + foreignTenant,
		Kind:     "generic",
		AuthMode: ingestion.AuthHMAC,
	})
	if err != nil {
		t.Fatal(err)
	}
	defaultSource, err := store.CreateSource(ctx, storage.CreateSourceInput{
		TenantID: tenantID,
		Name:     "default-scope-" + foreignTenant,
		Kind:     "generic",
		AuthMode: ingestion.AuthHMAC,
	})
	if err != nil {
		t.Fatal(err)
	}

	// This source is encrypted with a *different* master key from the API
	// store. A cross-tenant lookup must return 404 in SQL before trying to
	// decrypt it, rather than failing with a 500 and exposing key state.
	otherBox, err := cryptox.NewBox(bytes.Repeat([]byte{0xA5}, 32))
	if err != nil {
		t.Fatal(err)
	}
	otherStore, err := storage.Open(ctx, databaseURL(), otherBox, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(otherStore.Close)
	unreadable, err := otherStore.CreateSource(ctx, storage.CreateSourceInput{
		TenantID: tenantID,
		Name:     "other-key-" + foreignTenant,
		Kind:     "generic",
		AuthMode: ingestion.AuthHMAC,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), "DELETE FROM event_sources WHERE id=$1", unreadable.Source.ID)
	}()
	scoped, err := store.GetSourceCredentialsForTenant(ctx, foreignTenant, ownSource.Source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(scoped.HMACSecret) != ownSource.Credential {
		t.Fatal("own-tenant source credential did not decrypt")
	}
	if _, err := store.GetSourceCredentialsForTenant(ctx, foreignTenant, unreadable.Source.ID); !storage.IsNoRows(err) {
		t.Fatalf("cross-tenant lookup must return not-found before decrypt: %v", err)
	}
	if _, err := store.GetSourceCredentialsForTenant(ctx, "", ownSource.Source.ID); !storage.IsNoRows(err) {
		t.Fatalf("missing tenant must fail closed: %v", err)
	}
	if _, err := store.GetSourceCredentials(ctx, unreadable.Source.ID); err == nil || storage.IsNoRows(err) {
		t.Fatalf("unscoped lookup should fail to decrypt different-key fixture: %v", err)
	}

	account, err := store.CreateServiceAccount(ctx, storage.CreateServiceAccountInput{
		TenantID: foreignTenant,
		Name:     "isolation-admin",
		Roles:    []auth.Role{auth.RoleTenantAdmin},
		ActorID:  "integration",
	})
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateServiceAccountKey(ctx, storage.CreateServiceAccountKeyInput{
		TenantID:         foreignTenant,
		ServiceAccountID: account.ID,
		ActorID:          "integration",
	})
	if err != nil {
		t.Fatal(err)
	}

	suffix := fmt.Sprint(time.Now().UnixNano())
	bus, err := messaging.Connect(natsURL(), "TENANT_MANUAL_"+suffix, "stormrelay.tenant.manual."+suffix, "consumer_"+suffix, "tenant-boundary-integration")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)

	handler := api.New(config.Config{
		DefaultTenantID:             tenantID,
		BootstrapAPIKey:             "isolation-bootstrap-key",
		AllowUnauthenticatedSources: true,
	}, store, bus, &telemetry.Metrics{}, slog.Default()).Handler()

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
		if item.ID == ownSource.Source.ID {
			seenOwn = true
		}
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

	crossKeyTest := request(key.Credential, http.MethodPost, "/api/v1/sources/"+unreadable.Source.ID+"/test", "")
	if crossKeyTest.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant source with unreadable credential returned %d: %s", crossKeyTest.Code, crossKeyTest.Body.String())
	}


	// Disabling a source must also block its authenticated test endpoint;
	// otherwise that endpoint bypasses the normal webhook enabled guard.
	if _, err := conn.Exec(ctx, "UPDATE event_sources SET enabled=false WHERE id=$1", ownSource.Source.ID); err != nil {
		t.Fatal(err)
	}
	disabledTest := request(key.Credential, http.MethodPost, "/api/v1/sources/"+ownSource.Source.ID+"/test", "")
	if disabledTest.Code != http.StatusGone {
		t.Fatalf("disabled source test returned %d: %s", disabledTest.Code, disabledTest.Body.String())
	}

	manual := request(key.Credential, http.MethodPost, "/api/v1/incidents",
		`{"title":"must not write into bootstrap tenant","severity":"warning"}`)
	if manual.Code != http.StatusAccepted {
		t.Fatalf("manual tenant status=%d: %s", manual.Code, manual.Body.String())
	}
	foreignManualID, err := store.EnsureManualSource(ctx, foreignTenant)
	if err != nil {
		t.Fatal(err)
	}
	defaultManualID, err := store.EnsureManualSource(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if foreignManualID == defaultManualID {
		t.Fatalf("tenant manual sources share ID %s", foreignManualID)
	}
	manualWebhook := request(key.Credential, http.MethodPost, "/api/v1/webhooks/"+foreignManualID, "{}")
	if manualWebhook.Code != http.StatusNotFound {
		t.Fatalf("manual source externally reachable: %d", manualWebhook.Code)
	}
	manualTest := request(key.Credential, http.MethodPost, "/api/v1/sources/"+foreignManualID+"/test", "")
	if manualTest.Code != http.StatusNotFound {
		t.Fatalf("internal manual source exposed via source test: %d", manualTest.Code)
	}

	if r := request(key.Credential, http.MethodGet, "/api/v1/incidents", ""); r.Code != http.StatusOK {
		t.Fatalf("incident list status=%d: %s", r.Code, r.Body.String())
	}
}

// Concurrent first requests must converge on one tenant-private source.
func TestConcurrentManualSourceProvisioning(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	otherTenant, err := id.New()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := pgx.Connect(ctx, databaseURL())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "INSERT INTO tenants(id,slug,name) VALUES($1,$2,$3)", otherTenant, "manual-"+otherTenant, "Concurrent manual source tenant"); err != nil {
		t.Fatal(err)
	}
	const concurrent = 12
	ids := make(chan string, concurrent)
	errs := make(chan error, concurrent)
	var wg sync.WaitGroup
	for i := 0; i < concurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			created, err := store.EnsureManualSource(ctx, otherTenant)
			ids <- created
			errs <- err
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	first := ""
	for next := range ids {
		if first == "" {
			first = next
		}
		if first != next {
			t.Fatalf("manual source IDs differ: %s and %s", first, next)
		}
	}
	var total int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM event_sources WHERE tenant_id=$1 AND name='manual-api'", otherTenant).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 1 {
		t.Fatalf("manual source count=%d, want 1", total)
	}
	if _, err := store.CreateSource(ctx, storage.CreateSourceInput{TenantID: otherTenant, Name: "MANUAL-API", AuthMode: ingestion.AuthHMAC}); err == nil {
		t.Fatal("public source creation accepted reserved manual source name")
	}
}
