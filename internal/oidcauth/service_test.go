package oidcauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/coreos/go-oidc/v3/oidc"
)

type fakeIdentityStore struct {
	provider  storage.OIDCProvider
	principal auth.Principal
	findErr   error
	authErr   error
	seenSub   string
}

func (f *fakeIdentityStore) FindOIDCProvider(_ context.Context, issuer string, audiences []string) (storage.OIDCProvider, error) {
	if f.findErr != nil {
		return storage.OIDCProvider{}, f.findErr
	}
	if issuer != f.provider.Issuer {
		return storage.OIDCProvider{}, fmt.Errorf("unexpected issuer %q", issuer)
	}
	found := false
	for _, audience := range audiences {
		if audience == f.provider.Audience {
			found = true
		}
	}
	if !found {
		return storage.OIDCProvider{}, fmt.Errorf("expected audience missing: %v", audiences)
	}
	return f.provider, nil
}

func (f *fakeIdentityStore) AuthenticateOIDCIdentity(_ context.Context, providerID, subject string) (auth.Principal, error) {
	f.seenSub = subject
	if f.authErr != nil {
		return auth.Principal{}, f.authErr
	}
	if providerID != f.provider.ID {
		return auth.Principal{}, fmt.Errorf("unexpected provider %q", providerID)
	}
	return f.principal, nil
}

func TestAuthenticateSignedToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := storage.OIDCProvider{
		ID: "provider-1", TenantID: "tenant-1", Issuer: "https://issuer.example.com",
		Audience: "stormrelay-api", JWKSURI: "https://keys.example.com/jwks", SupportedSigningAlgs: []string{oidc.RS256}, Enabled: true, Version: 1,
	}
	principal := auth.Principal{TenantID: "tenant-1", ActorType: "oidc-user", ActorID: "user-1", Roles: []auth.Role{auth.RoleResponder}}
	store := &fakeIdentityStore{provider: provider, principal: principal}
	factoryCalls := 0
	service := newService(store, func(_ context.Context, got storage.OIDCProvider) (tokenVerifier, error) {
		factoryCalls++
		if got.ID != provider.ID {
			t.Fatalf("provider=%+v", got)
		}
		return oidc.NewVerifier(provider.Issuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&privateKey.PublicKey}}, &oidc.Config{
			ClientID: provider.Audience, SupportedSigningAlgs: []string{oidc.RS256},
		}), nil
	})

	token := signRS256(t, privateKey, map[string]any{
		"iss": provider.Issuer, "aud": []string{"other", provider.Audience}, "sub": "subject-123",
		"iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	got, err := service.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActorID != principal.ActorID || got.TenantID != principal.TenantID || store.seenSub != "subject-123" {
		t.Fatalf("principal=%+v subject=%q", got, store.seenSub)
	}
	if _, err := service.Authenticate(context.Background(), token); err != nil {
		t.Fatal(err)
	}
	if factoryCalls != 1 {
		t.Fatalf("factory calls=%d", factoryCalls)
	}
}

func TestAuthenticateRejectsExpiredAndWrongAudience(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := storage.OIDCProvider{ID: "p", Issuer: "https://issuer.example.com", Audience: "expected", SupportedSigningAlgs: []string{oidc.RS256}, Version: 1}
	store := &fakeIdentityStore{provider: provider, principal: auth.Principal{TenantID: "t", ActorID: "u", Roles: []auth.Role{auth.RoleViewer}}}
	service := newService(store, func(context.Context, storage.OIDCProvider) (tokenVerifier, error) {
		return oidc.NewVerifier(provider.Issuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&privateKey.PublicKey}}, &oidc.Config{ClientID: provider.Audience, SupportedSigningAlgs: []string{oidc.RS256}}), nil
	})

	for name, claims := range map[string]map[string]any{
		"expired":        {"iss": provider.Issuer, "aud": provider.Audience, "sub": "s", "exp": time.Now().Add(-time.Minute).Unix()},
		"wrong-audience": {"iss": provider.Issuer, "aud": []string{provider.Audience, "routing-only"}, "sub": "s", "exp": time.Now().Add(time.Hour).Unix(), "azp": "wrong"},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "wrong-audience" {
				claims["aud"] = "routing-only"
				store.provider.Audience = "routing-only"
				defer func() { store.provider.Audience = provider.Audience }()
			}
			token := signRS256(t, privateKey, claims)
			_, err := service.Authenticate(context.Background(), token)
			if !errors.Is(err, ErrInvalidCredential) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestParseUnverifiedRoutingClaims(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"iss": "https://issuer.example.com", "aud": []string{"a", "b", "a"}})
	token := "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
	claims, err := parseUnverifiedRoutingClaims(token)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Issuer != "https://issuer.example.com" || len(claims.Audiences) != 2 {
		t.Fatalf("claims=%+v", claims)
	}
	for _, malformed := range []string{"", "a.b", "a.!!!.c", makeRoutingToken("http://insecure.example", "aud")} {
		if _, err := parseUnverifiedRoutingClaims(malformed); err == nil {
			t.Fatalf("accepted %q", malformed)
		}
	}
}

func TestSafeSigningAlgs(t *testing.T) {
	got := safeSigningAlgs([]string{"rs256", "none", "RS256", "HS256", "ES256"})
	if len(got) != 2 || got[0] != oidc.RS256 || got[1] != oidc.ES256 {
		t.Fatalf("algs=%v", got)
	}
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": "test-key", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func makeRoutingToken(issuer, audience string) string {
	payload, _ := json.Marshal(map[string]any{"iss": issuer, "aud": audience})
	return "e30." + base64.RawURLEncoding.EncodeToString(payload) + ".x"
}
