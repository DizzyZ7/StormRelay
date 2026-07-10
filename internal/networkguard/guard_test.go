package networkguard

import (
	"context"
	"testing"
)

func TestRejectsNonAllowlistedHost(t *testing.T) {
	guard := New([]string{"allowed.example"})
	if _, err := guard.Resolve(context.Background(), "https://metadata.invalid/latest"); err == nil {
		t.Fatal("expected host allowlist rejection")
	}
}

func TestRejectsUserInfo(t *testing.T) {
	guard := New([]string{"example.com"})
	if _, err := guard.Resolve(context.Background(), "https://user:pass@example.com/path"); err == nil {
		t.Fatal("expected userinfo rejection")
	}
}
