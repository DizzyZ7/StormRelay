package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestServiceAccountCredentialFormatAndHash(t *testing.T) {
	credential, prefix, hash, err := newServiceAccountCredential()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(credential, "srk_"+prefix+"_") {
		t.Fatalf("credential=%q prefix=%q", credential, prefix)
	}
	if len(prefix) != 16 {
		t.Fatalf("prefix length=%d", len(prefix))
	}
	if _, err := hex.DecodeString(prefix); err != nil {
		t.Fatalf("prefix is not hex: %v", err)
	}
	actual := sha256.Sum256([]byte(credential))
	if string(hash) != string(actual[:]) {
		t.Fatal("stored hash does not match complete credential")
	}
	parsed, ok := serviceAccountKeyPrefix(credential)
	if !ok || parsed != prefix {
		t.Fatalf("parsed=%q ok=%v", parsed, ok)
	}
}

func TestServiceAccountCredentialRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "srk_short_secret", "srk_zzzzzzzzzzzzzzzz_secret", "other_0123456789abcdef_secret"} {
		if _, ok := serviceAccountKeyPrefix(value); ok {
			t.Fatalf("accepted malformed credential %q", value)
		}
	}
}
