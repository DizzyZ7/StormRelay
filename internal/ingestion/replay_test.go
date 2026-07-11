package ingestion

import (
	"testing"
	"time"
)

func TestReplayCacheRejectsCommittedReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cache := NewReplayCache(1000)
	if !cache.Accept("signed-request", now.Add(5*time.Minute), now) {
		t.Fatal("first reservation was rejected")
	}
	if cache.Accept("signed-request", now.Add(5*time.Minute), now.Add(time.Second)) {
		t.Fatal("active replay reservation was accepted twice")
	}
}

func TestReplayCacheReleaseAllowsRetryBeforeDurableAcceptance(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cache := NewReplayCache(1000)
	if !cache.Accept("signed-request", now.Add(5*time.Minute), now) {
		t.Fatal("first reservation was rejected")
	}
	cache.Release("signed-request")
	if !cache.Accept("signed-request", now.Add(5*time.Minute), now.Add(time.Second)) {
		t.Fatal("released reservation did not allow a retry")
	}
}

func TestReplayCacheExpiredReservationCanBeReused(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cache := NewReplayCache(1000)
	if !cache.Accept("signed-request", now.Add(time.Second), now) {
		t.Fatal("first reservation was rejected")
	}
	if !cache.Accept("signed-request", now.Add(5*time.Minute), now.Add(2*time.Second)) {
		t.Fatal("expired reservation was not reusable")
	}
}
