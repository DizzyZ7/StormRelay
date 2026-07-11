package ingestion

import (
	"sync"
	"time"
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

// Accept reserves a replay key until expires. Call Release when processing fails
// before the request has been durably accepted. A durable acceptance keeps the
// reservation until its normal expiry so an identical signed request is rejected.
func (c *ReplayCache) Accept(key string, expires time.Time, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.seen[key]; ok && old.After(now) {
		return false
	}
	if len(c.seen) >= c.max {
		for k, v := range c.seen {
			if !v.After(now) {
				delete(c.seen, k)
			}
		}
		if len(c.seen) >= c.max {
			for k := range c.seen {
				delete(c.seen, k)
				break
			}
		}
	}
	c.seen[key] = expires
	return true
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
