package ingestion

import (
	"sync"
	"time"
)

type bucket struct {
	tokens float64
	last   time.Time
}
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]bucket
	rate    float64
	burst   float64
}

func NewLimiter(ratePerSecond float64, burst int) *Limiter {
	return &Limiter{buckets: map[string]bucket{}, rate: ratePerSecond, burst: float64(burst)}
}
func (l *Limiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b.last.IsZero() {
		b.tokens = l.burst
		b.last = now
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		l.buckets[key] = b
		return false
	}
	b.tokens--
	l.buckets[key] = b
	return true
}
