//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/storage"
)

func TestOIDCProviderSelectionRejectsAmbiguousAudienceSet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	issuer := fmt.Sprintf("https://multi-audience-%d.example.com", suffix)

	for index, audience := range []string{
		fmt.Sprintf("audience-a-%d", suffix),
		fmt.Sprintf("audience-b-%d", suffix),
	} {
		if _, err := s.CreateOIDCProvider(ctx, storage.CreateOIDCProviderInput{
			TenantID: tenantID, Name: fmt.Sprintf("multi-provider-%d-%d", suffix, index),
			Issuer: issuer, Audience: audience,
			JWKSURI: fmt.Sprintf("https://multi-keys-%d-%d.example.com/jwks", suffix, index),
			SupportedSigningAlgs: []string{"RS256"}, ActorID: "integration-admin",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.FindOIDCProvider(ctx, issuer, []string{
		fmt.Sprintf("audience-a-%d", suffix),
		fmt.Sprintf("audience-b-%d", suffix),
	}); !storage.IsNoRows(err) {
		t.Fatalf("ambiguous provider selection error=%v", err)
	}
}
