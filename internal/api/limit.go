package api

import (
	"sync"
	"time"
)

// limiter allows a number of events per window for each key. Keys are
// network prefixes and live only in memory.
type limiter struct {
	max    int
	window time.Duration
	mu     sync.Mutex
	seen   map[string][]time.Time
	now    func() time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, seen: map[string][]time.Time{}, now: time.Now}
}

// allow records an event and reports whether it is within the limit.
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	kept := l.seen[key][:0]
	for _, t := range l.seen[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.seen[key] = kept
		return false
	}
	l.seen[key] = append(kept, now)
	// Forget keys that went quiet, so memory stays small.
	if len(l.seen) > 50000 {
		for k, ts := range l.seen {
			if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
				delete(l.seen, k)
			}
		}
	}
	return true
}
