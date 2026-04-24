package auth

import (
	"math/rand"
	"sync"
	"time"
)

// Group-level downtime schedules. Currently only the "new" group has a
// schedule: 10 random whole-hour windows per local day during which its
// credentials are withheld from routing. Hours are chosen deterministically
// from the date (and group name) so every server instance picks the same
// set on the same day; there is no shared state across restarts beyond the
// clock itself.
//
// Enforcement point: oauthUsableLocked + the API-key iteration in
// Pool.Acquire (and Pool.HasAPIKeyFor for the fail-fast check). A
// credential in an idle hour behaves as if it were temporarily quota-
// exceeded — it simply isn't picked, so the retry loop falls through to
// other groups / public credentials if any exist, or the client sees 503.
//
// Adding more groups: return a non-nil set from groupIdleHoursForDate.

const groupNewIdleHoursPerDay = 10

var groupSchedules = struct {
	sync.Mutex
	cache map[string]map[int]bool // key = "group|YYYY-MM-DD"
}{cache: make(map[string]map[int]bool)}

// groupIdleHoursForDate returns the set of hours-of-day (0..23) during which
// credentials in `group` should refuse to route on the given local date.
// Returns nil when the group has no schedule.
func groupIdleHoursForDate(group string, date time.Time) map[int]bool {
	if group != "new" {
		return nil
	}
	key := group + "|" + date.Format("2006-01-02")
	groupSchedules.Lock()
	defer groupSchedules.Unlock()
	if m, ok := groupSchedules.cache[key]; ok {
		return m
	}
	// Seed: date component keeps the schedule stable across the day; group
	// name hashed in so future groups don't collide on the same hours.
	y, mo, d := date.Date()
	seed := int64(y)*10000 + int64(mo)*100 + int64(d)
	for _, c := range group {
		seed = seed*131 + int64(c)
	}
	r := rand.New(rand.NewSource(seed))
	hours := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}
	r.Shuffle(len(hours), func(i, j int) { hours[i], hours[j] = hours[j], hours[i] })
	idle := make(map[int]bool, groupNewIdleHoursPerDay)
	for i := 0; i < groupNewIdleHoursPerDay && i < len(hours); i++ {
		idle[hours[i]] = true
	}
	// Bound cache size — one entry per group per day is tiny, but a very
	// long-lived process still shouldn't leak unboundedly.
	if len(groupSchedules.cache) > 32 {
		for k := range groupSchedules.cache {
			if k != key {
				delete(groupSchedules.cache, k)
			}
		}
	}
	groupSchedules.cache[key] = idle
	return idle
}

// isGroupIdleNow reports whether `group`'s scheduled downtime covers the
// current local hour. Always false for groups without a schedule.
func isGroupIdleNow(group string, now time.Time) bool {
	if group == "" {
		return false
	}
	idle := groupIdleHoursForDate(group, now)
	if idle == nil {
		return false
	}
	return idle[now.Hour()]
}
