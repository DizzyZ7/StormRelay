package ingestion

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/cryptox"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrReplay       = errors.New("request timestamp outside replay window")
)

type AuthMode string

const (
	AuthNone   AuthMode = "none"
	AuthHMAC   AuthMode = "hmac-sha256"
	AuthBearer AuthMode = "bearer"
)

type Credentials struct {
	Mode       AuthMode
	HMACSecret []byte
	BearerHash []byte
}

type VerifyInput struct {
	Credentials   Credentials
	Body          []byte
	Signature     string
	Authorization string
	Timestamp     string
	Now           time.Time
	ReplayWindow  time.Duration
}

func Verify(in VerifyInput) error {
	if in.Now.IsZero() {
		in.Now = time.Now().UTC()
	}
	switch in.Credentials.Mode {
	case AuthNone:
		return nil
	case AuthBearer:
		const prefix = "Bearer "
		if !strings.HasPrefix(in.Authorization, prefix) {
			return ErrUnauthorized
		}
		provided := strings.TrimSpace(strings.TrimPrefix(in.Authorization, prefix))
		candidate := cryptox.HashSecret(provided)
		if len(in.Credentials.BearerHash) != len(candidate) || subtle.ConstantTimeCompare(in.Credentials.BearerHash, candidate[:]) != 1 {
			return ErrUnauthorized
		}
		return nil
	case AuthHMAC:
		unix, err := strconv.ParseInt(in.Timestamp, 10, 64)
		if err != nil {
			return ErrReplay
		}
		when := time.Unix(unix, 0)
		if in.Now.Sub(when) > in.ReplayWindow || when.Sub(in.Now) > in.ReplayWindow {
			return ErrReplay
		}
		provided := strings.TrimPrefix(strings.TrimSpace(in.Signature), "sha256=")
		sig, err := hex.DecodeString(provided)
		if err != nil {
			return ErrUnauthorized
		}
		mac := hmac.New(sha256.New, in.Credentials.HMACSecret)
		_, _ = fmt.Fprintf(mac, "%s.", in.Timestamp)
		_, _ = mac.Write(in.Body)
		if !hmac.Equal(sig, mac.Sum(nil)) {
			return ErrUnauthorized
		}
		return nil
	default:
		return ErrUnauthorized
	}
}
