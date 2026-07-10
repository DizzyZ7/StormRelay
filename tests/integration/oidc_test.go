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

func TestOIDCProviderIdentityLifecycle(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	issuer := fmt.Sprintf("https://issuer-%d.example.com", suffix)
	audience := fmt.Sprintf("stormrelay-%d", suffix)

	provider, err := s.CreateOIDCProvider(ctx, storage.CreateOIDCProviderInput{
		TenantID: tenantID, Name: fmt.Sprintf("provider-%d", suffix), Issuer: issuer,
		Audience: audience, JWKSURI: fmt.Sprintf("https://keys-%d.example.com/jwks", suffix),
		SupportedSigningAlgs: []string{"RS256", "ES256"}, ActorID: "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if provider.Version != 1 || !provider.Enabled || len(provider.SupportedSigningAlgs) != 2 {
		t.Fatalf("provider=%+v", provider)
	}

	found, err := s.FindOIDCProvider(ctx, issuer, []string{"other", audience})
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != provider.ID || found.TenantID != tenantID {
		t.Fatalf("found=%+v", found)
	}

	identity, err := s.CreateOIDCIdentity(ctx, storage.CreateOIDCIdentityInput{
		TenantID: tenantID, ProviderID: provider.ID, Subject: "subject-123",
		Email: fmt.Sprintf("oidc-%d@example.com", suffix), DisplayName: "OIDC Integration User",
		Roles: []auth.Role{auth.RoleViewer}, ActorID: "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err := s.AuthenticateOIDCIdentity(ctx, provider.ID, identity.Subject)
	if err != nil {
		t.Fatal(err)
	}
	if principal.TenantID != tenantID || principal.ActorID != identity.UserID || !principal.Allowed(auth.PermissionIncidentsRead) || principal.Allowed(auth.PermissionIncidentsWrite) {
		t.Fatalf("principal=%+v", principal)
	}

	identity, err = s.UpdateOIDCIdentity(ctx, storage.UpdateOIDCIdentityInput{
		TenantID: tenantID, ID: identity.ID, Enabled: true, Roles: []auth.Role{auth.RoleResponder},
		ExpectedVersion: identity.Version, ActorID: "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	principal, err = s.AuthenticateOIDCIdentity(ctx, provider.ID, identity.Subject)
	if err != nil {
		t.Fatal(err)
	}
	if !principal.Allowed(auth.PermissionApprovalsDecide) {
		t.Fatalf("roles=%v", principal.Roles)
	}

	if _, err := s.UpdateOIDCIdentity(ctx, storage.UpdateOIDCIdentityInput{
		TenantID: tenantID, ID: identity.ID, Enabled: true, Roles: identity.Roles,
		ExpectedVersion: identity.Version - 1, ActorID: "integration-admin",
	}); err == nil {
		t.Fatal("expected identity version conflict")
	}

	identity, err = s.UpdateOIDCIdentity(ctx, storage.UpdateOIDCIdentityInput{
		TenantID: tenantID, ID: identity.ID, Enabled: false, Roles: identity.Roles,
		ExpectedVersion: identity.Version, ActorID: "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthenticateOIDCIdentity(ctx, provider.ID, identity.Subject); !storage.IsNoRows(err) {
		t.Fatalf("disabled identity error=%v", err)
	}

	provider, err = s.UpdateOIDCProvider(ctx, storage.UpdateOIDCProviderInput{
		TenantID: tenantID, ID: provider.ID, Name: provider.Name, Enabled: false,
		ExpectedVersion: provider.Version, ActorID: "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.FindOIDCProvider(ctx, issuer, []string{audience}); !storage.IsNoRows(err) {
		t.Fatalf("disabled provider lookup error=%v", err)
	}

	items, err := s.ListOIDCIdentities(ctx, tenantID, provider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != identity.ID || items[0].Email == "" {
		t.Fatalf("identities=%+v", items)
	}
	if items, err := s.ListOIDCProviders(ctx, "00000000-0000-4000-8000-000000000099"); err != nil || len(items) != 0 {
		t.Fatalf("cross-tenant providers=%+v error=%v", items, err)
	}
}

func TestOIDCIdentityRejectsDuplicateEmailAutoLinking(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	provider, err := s.CreateOIDCProvider(ctx, storage.CreateOIDCProviderInput{
		TenantID: tenantID, Name: fmt.Sprintf("duplicate-provider-%d", suffix),
		Issuer: fmt.Sprintf("https://duplicate-%d.example.com", suffix), Audience: fmt.Sprintf("aud-%d", suffix),
		JWKSURI: fmt.Sprintf("https://duplicate-keys-%d.example.com/jwks", suffix), SupportedSigningAlgs: []string{"RS256"}, ActorID: "integration-admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	email := fmt.Sprintf("duplicate-%d@example.com", suffix)
	if _, err := s.CreateOIDCIdentity(ctx, storage.CreateOIDCIdentityInput{
		TenantID: tenantID, ProviderID: provider.ID, Subject: "first", Email: email,
		DisplayName: "First", Roles: []auth.Role{auth.RoleViewer}, ActorID: "integration-admin",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateOIDCIdentity(ctx, storage.CreateOIDCIdentityInput{
		TenantID: tenantID, ProviderID: provider.ID, Subject: "second", Email: email,
		DisplayName: "Second", Roles: []auth.Role{auth.RoleViewer}, ActorID: "integration-admin",
	}); err == nil {
		t.Fatal("expected duplicate email to require explicit account-linking workflow")
	}
}
