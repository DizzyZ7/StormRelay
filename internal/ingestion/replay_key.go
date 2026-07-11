package ingestion

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// CanonicalHMACReplayKey returns one stable replay identity for every accepted
// textual representation of the same signed request. Timestamp leading signs
// and zeroes, surrounding whitespace, an optional sha256= prefix, and hex case
// therefore cannot create distinct replay-cache entries.
func CanonicalHMACReplayKey(sourceID, timestamp, signature string) (string, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return "", fmt.Errorf("source ID is required")
	}

	unix, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid replay timestamp: %w", err)
	}

	encodedSignature := strings.TrimSpace(signature)
	encodedSignature = strings.TrimPrefix(encodedSignature, "sha256=")
	digest, err := hex.DecodeString(encodedSignature)
	if err != nil || len(digest) != sha256.Size {
		return "", fmt.Errorf("invalid HMAC signature encoding")
	}

	return sourceID + ":" + strconv.FormatInt(unix, 10) + ":" + hex.EncodeToString(digest), nil
}
