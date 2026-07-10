package telemetry

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

func ParseTraceParent(value string) (traceID string, valid bool) {
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return "", false
	}
	if _, err := hex.DecodeString(parts[1] + parts[2] + parts[3]); err != nil || parts[1] == strings.Repeat("0", 32) {
		return "", false
	}
	return parts[1], true
}
func NewTraceParent() (string, string) {
	trace := make([]byte, 16)
	span := make([]byte, 8)
	_, _ = rand.Read(trace)
	_, _ = rand.Read(span)
	traceID := hex.EncodeToString(trace)
	return "00-" + traceID + "-" + hex.EncodeToString(span) + "-01", traceID
}
