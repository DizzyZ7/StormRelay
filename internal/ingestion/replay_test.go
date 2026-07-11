package ingestion

import (
	"fmt"
	"testing"
	"time"
)

func TestReplayCacheRejectsCommittedReplay(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cache := NewReplayCache(1000)
	if cache.Reserve("signed-request", now.Add(5*time.Minute), now) != ReplayReserved {
		t.Fatal("first reservation was rejected")
	}
	if cache.Reserve("signed-request", now.Add(5*time.Minute), now.Add(time.Second)) != ReplayDuplicate {
		t.Fatal("active replay reservation was not reported as duplicate")
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

func TestReplayCacheDoesNotEvictActiveReservationsAtCapacity(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cache := NewReplayCache(1000)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("active-%04d", i)
		if result := cache.Reserve(key, now.Add(5*time.Minute), now); result != ReplayReserved {
			t.Fatalf("reservation %d result=%v", i, result)
		}
	}
	if result := cache.Reserve("new-request", now.Add(5*time.Minute), now); result != ReplayCapacityExceeded {
		t.Fatalf("full cache result=%v, want capacity exceeded", result)
	}
	if result := cache.Reserve("active-0000", now.Add(5*time.Minute), now); result != ReplayDuplicate {
		t.Fatalf("old active reservation was evicted: result=%v", result)
	}
}

func TestReplayCachePrunesExpiredEntriesBeforeCapacityFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	cache := NewReplayCache(1000)
	for i := 0; i < 1000; i++ {
		expires := now.Add(5 * time.Minute)
		if i == 0 {
			expires = now.Add(time.Second)
		}
		if result := cache.Reserve(fmt.Sprintf("key-%04d", i), expires, now); result != ReplayReserved {
			t.Fatalf("reservation %d result=%v", i, result)
		}
	}
	later := now.Add(2 * time.Second)
	if result := cache.Reserve("replacement", later.Add(5*time.Minute), later); result != ReplayReserved {
		t.Fatalf("replacement result=%v", result)
	}
	if result := cache.Reserve("key-0001", later.Add(5*time.Minute), later); result != ReplayDuplicate {
		t.Fatalf("unexpired reservation was lost: result=%v", result)
	}
}
