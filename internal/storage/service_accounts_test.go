package storage

import (
	"crypto/sha256"
	"encoding/base64"
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

func TestServiceAccountCredentialAllowsUnderscoresInSecret(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = 0xff
	}
	encoded := base64.RawURLEncoding.EncodeToString(secret)
	if !strings.Contains(encoded, "_") {
		t.Fatalf("test secret unexpectedly lacks underscore: %q", encoded)
	}
	credential := "srk_0123456789abcdef_" + encoded
	prefix, ok := serviceAccountKeyPrefix(credential)
	if !ok || prefix != "0123456789abcdef" {
		t.Fatalf("prefix=%q ok=%v", prefix, ok)
	}
}

func TestServiceAccountCredentialRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{
		"",
		"srk_short_secret",
		"srk_zzzzzzzzzzzzzzzz_secret",
		"other_0123456789abcdef_secret",
		"srk_0123456789abcdef_not-base64!",
		"srk_0123456789abcdef_" + base64.RawURLEncoding.EncodeToString(make([]byte, 31)),
	} {
		if _, ok := serviceAccountKeyPrefix(value); ok {
			t.Fatalf("accepted malformed credential %q", value)
		}
	}
}
