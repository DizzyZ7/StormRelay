package ingestion

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestVerifyHMACAndReplayWindow(t *testing.T) {
	now := time.Unix(1700000000, 0)
	ts := strconv.FormatInt(now.Unix(), 10)
	body := []byte(`{"x":1}`)
	secret := []byte("secret")
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	input := VerifyInput{Credentials: Credentials{Mode: AuthHMAC, HMACSecret: secret}, Body: body, Timestamp: ts, Signature: "sha256=" + hex.EncodeToString(mac.Sum(nil)), Now: now, ReplayWindow: 5 * time.Minute}
	if err := Verify(input); err != nil {
		t.Fatal(err)
	}
	input.Now = now.Add(6 * time.Minute)
	if err := Verify(input); !errors.Is(err, ErrReplay) {
		t.Fatalf("got %v", err)
	}
}
