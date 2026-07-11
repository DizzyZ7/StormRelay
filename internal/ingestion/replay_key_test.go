package ingestion

import (
	"strings"
	"testing"
)

func TestCanonicalHMACReplayKeyNormalizesAcceptedRepresentations(t *testing.T) {
	lower := strings.Repeat("ab", 32)
	upper := strings.ToUpper(lower)

	first, err := CanonicalHMACReplayKey(" source-id ", "001700000000", " sha256="+upper+" ")
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalHMACReplayKey("source-id", "1700000000", lower)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("canonical keys differ:\n%s\n%s", first, second)
	}
	want := "source-id:1700000000:" + lower
	if first != want {
		t.Fatalf("key=%q, want %q", first, want)
	}
}

func TestCanonicalHMACReplayKeyRejectsMalformedInputs(t *testing.T) {
	valid := strings.Repeat("ab", 32)
	for name, sourceID, timestamp, signature := range map[string][3]string{
		"empty source":      {"", "1700000000", valid},
		"invalid timestamp": {"source-id", "not-a-time", valid},
		"invalid hex":       {"source-id", "1700000000", "zz"},
		"short digest":      {"source-id", "1700000000", "ab"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := CanonicalHMACReplayKey(sourceID, timestamp, signature); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
