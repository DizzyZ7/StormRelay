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
