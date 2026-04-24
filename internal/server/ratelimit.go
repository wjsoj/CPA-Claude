package server

import (
	"sync"
	"time"
)

// rpmLimiter enforces a sliding 60-second requests-per-minute cap per key.
// Each key gets its own *rpmBucket stored in a sync.Map; entries stay
// resident for the life of the process (bounded by the number of distinct
// client-token × provider pairs we ever see — small in practice).
type rpmLimiter struct {
	buckets sync.Map // map[string]*rpmBucket
}

type rpmBucket struct {
	mu     sync.Mutex
	stamps []time.Time // request times inside the current window, oldest first
}

const rpmWindow = time.Minute

// allow records an attempt for key against limit. Returns (true, 0) when the
// request fits in the last-minute window; (false, retryAfterSec) when it
// would exceed the cap. limit<=0 disables the check (allow always true).
// retryAfterSec is the whole seconds until the oldest in-window stamp ages
// out (minimum 1).
func (l *rpmLimiter) allow(key string, limit int) (bool, int) {
	if limit <= 0 {
		return true, 0
	}
	v, _ := l.buckets.LoadOrStore(key, &rpmBucket{})
	b := v.(*rpmBucket)

	now := time.Now()
	cutoff := now.Add(-rpmWindow)

	b.mu.Lock()
	defer b.mu.Unlock()

	// Drop stamps that have aged out of the window.
	drop := 0
	for drop < len(b.stamps) && b.stamps[drop].Before(cutoff) {
		drop++
	}
	if drop > 0 {
		b.stamps = b.stamps[drop:]
	}

	if len(b.stamps) >= limit {
		// Oldest stamp + window = earliest moment a new slot opens up.
		wait := b.stamps[0].Add(rpmWindow).Sub(now)
		sec := int(wait / time.Second)
		if wait%time.Second > 0 {
			sec++
		}
		if sec < 1 {
			sec = 1
		}
		return false, sec
	}
	b.stamps = append(b.stamps, now)
	return true, 0
}
