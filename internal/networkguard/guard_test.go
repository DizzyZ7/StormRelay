package networkguard

import (
	"context"
	"net"
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

func TestProhibitedIPRanges(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"100.64.0.1",
		"169.254.169.254",
		"::1",
		"fd00::1",
		"fe80::1",
	} {
		if !prohibitedIP(net.ParseIP(raw)) {
			t.Fatalf("expected %s to be prohibited", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if prohibitedIP(net.ParseIP(raw)) {
			t.Fatalf("expected %s to be permitted", raw)
		}
	}
}
