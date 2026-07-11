package ingestion

import (
	"sync"
	"time"
)

type ReplayReservation uint8

const (
	ReplayReserved ReplayReservation = iota
	ReplayDuplicate
	ReplayCapacityExceeded
)

type ReplayCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
	max  int
}

func NewReplayCache(max int) *ReplayCache {
	if max < 1000 {
		max = 1000
	}
	return &ReplayCache{seen: map[string]time.Time{}, max: max}
}

// Reserve stores a replay key until expires. Call Release when processing fails
// before the request has been durably accepted. Active reservations are never
// evicted to admit new requests: when capacity remains exhausted after expired
// entries are pruned, Reserve fails closed with ReplayCapacityExceeded.
func (c *ReplayCache) Reserve(key string, expires time.Time, now time.Time) ReplayReservation {
	c.mu.Lock()
	defer c.mu.Unlock()

	if old, ok := c.seen[key]; ok && old.After(now) {
		return ReplayDuplicate
	}
	if len(c.seen) >= c.max {
		for candidate, expiry := range c.seen {
			if !expiry.After(now) {
				delete(c.seen, candidate)
			}
		}
		if len(c.seen) >= c.max {
			return ReplayCapacityExceeded
		}
	}
	c.seen[key] = expires
	return ReplayReserved
}

// Accept is retained for callers that only need accepted/rejected semantics.
func (c *ReplayCache) Accept(key string, expires time.Time, now time.Time) bool {
	return c.Reserve(key, expires, now) == ReplayReserved
}

// Release removes a reservation after a request failed before its durable
// acceptance boundary. It intentionally does nothing for an empty key.
func (c *ReplayCache) Release(key string) {
	if key == "" {
		return
	}
	c.mu.Lock()
	delete(c.seen, key)
	c.mu.Unlock()
}
