package oidcauth

import (
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
)

func TestDiscoverySigningAlgsDefaultsOnlyWhenOmitted(t *testing.T) {
	algs, err := discoverySigningAlgs(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(algs) != 1 || algs[0] != oidc.RS256 {
		t.Fatalf("algs=%v", algs)
	}
}

func TestDiscoverySigningAlgsRejectsExplicitUnsupportedSet(t *testing.T) {
	if _, err := discoverySigningAlgs([]string{"none", "HS256"}); err == nil {
		t.Fatal("expected unsupported discovery algorithms to be rejected")
	}
	algs, err := discoverySigningAlgs([]string{"HS256", "ES256", "RS256"})
	if err != nil {
		t.Fatal(err)
	}
	if len(algs) != 2 || algs[0] != oidc.ES256 || algs[1] != oidc.RS256 {
		t.Fatalf("algs=%v", algs)
	}
}
