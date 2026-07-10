//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/storage"
)

func TestServiceAccountKeyLifecycleAndRoles(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	name := fmt.Sprintf("identity-%d", time.Now().UnixNano())

	account, err := s.CreateServiceAccount(ctx, storage.CreateServiceAccountInput{
		TenantID: tenantID,
		Name:     name,
		Roles:    []auth.Role{auth.RoleViewer},
		ActorID:  "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if account.Version != 1 || len(account.Roles) != 1 || account.Roles[0] != auth.RoleViewer {
		t.Fatalf("unexpected account: %+v", account)
	}

	created, err := s.CreateServiceAccountKey(ctx, storage.CreateServiceAccountKeyInput{
		TenantID:         tenantID,
		ServiceAccountID: account.ID,
		ActorID:          "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Credential == "" || created.Key.KeyPrefix == "" {
		t.Fatalf("credential was not returned once: %+v", created)
	}

	principal, err := s.AuthenticateServiceAccountKey(ctx, created.Credential)
	if err != nil {
		t.Fatal(err)
	}
	if principal.TenantID != tenantID || principal.ActorID != account.ID || principal.ActorType != "service-account" {
		t.Fatalf("unexpected principal: %+v", principal)
	}
	if !principal.Allowed(auth.PermissionIncidentsRead) || principal.Allowed(auth.PermissionIncidentsWrite) {
		t.Fatalf("viewer permissions are incorrect: %+v", principal.Roles)
	}

	keys, err := s.ListServiceAccountKeys(ctx, tenantID, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].KeyPrefix != created.Key.KeyPrefix {
		t.Fatalf("unexpected key metadata: %+v", keys)
	}

	account, err = s.UpdateServiceAccount(ctx, storage.UpdateServiceAccountInput{
		TenantID:        tenantID,
		ID:              account.ID,
		Name:            account.Name,
		Enabled:         true,
		Roles:           []auth.Role{auth.RoleResponder},
		ExpectedVersion: account.Version,
		ActorID:         "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err = s.AuthenticateServiceAccountKey(ctx, created.Credential)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Allowed(auth.PermissionIncidentsWrite) || !principal.Allowed(auth.PermissionApprovalsDecide) {
		t.Fatalf("updated responder permissions are missing: %+v", principal.Roles)
	}

	if _, err := s.RevokeServiceAccountKey(ctx, tenantID, created.Key.ID, "integration-admin", "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateServiceAccountKey(ctx, created.Credential); !storage.IsNoRows(err) {
		t.Fatalf("revoked key authentication error=%v", err)
	}

	second, err := s.CreateServiceAccountKey(ctx, storage.CreateServiceAccountKeyInput{
		TenantID:         tenantID,
		ServiceAccountID: account.ID,
		ActorID:          "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	account, err = s.UpdateServiceAccount(ctx, storage.UpdateServiceAccountInput{
		TenantID:        tenantID,
		ID:              account.ID,
		Name:            account.Name,
		Enabled:         false,
		Roles:           account.Roles,
		ExpectedVersion: account.Version,
		ActorID:         "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateServiceAccountKey(ctx, second.Credential); !storage.IsNoRows(err) {
		t.Fatalf("disabled account authentication error=%v", err)
	}

	if _, err := s.GetServiceAccount(ctx, "00000000-0000-4000-8000-000000000099", account.ID); !storage.IsNoRows(err) {
		t.Fatalf("cross-tenant lookup error=%v", err)
	}
}

func TestServiceAccountOptimisticVersionConflict(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	account, err := s.CreateServiceAccount(ctx, storage.CreateServiceAccountInput{
		TenantID: tenantID,
		Name:     fmt.Sprintf("version-%d", time.Now().UnixNano()),
		Roles:    []auth.Role{auth.RoleOperator},
		ActorID:  "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateServiceAccount(ctx, storage.UpdateServiceAccountInput{
		TenantID:        tenantID,
		ID:              account.ID,
		Name:            account.Name,
		Enabled:         true,
		Roles:           account.Roles,
		ExpectedVersion: account.Version + 1,
		ActorID:         "integration-admin",
	})
	if err == nil {
		t.Fatal("expected optimistic version conflict")
	}
}
