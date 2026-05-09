package server

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"
)

// switchTracker remembers which upstream credential last handled each
// (clientToken, conversation) pair so we can detect mid-conversation
// account rotation. The signal is needed by the thinking-block sanitizer:
// when account A's signed thinking blocks are about to be sent to account
// B, B's verifier returns 400.
//
// Conversation identity is the sha256 of the first user message — the
// same anchor SessionIDFor uses to keep multi-turn requests on a stable
// session_id. New topics rotate this hash, which correctly forces a
// "switch" decision on a freshly-acquired account too (no prior thinking
// blocks to worry about there, but the bookkeeping stays consistent).
//
// Why per (clientToken, convKey) and not per clientToken alone — one
// downstream client token may run several concurrent conversations; a
// switch on one shouldn't be silently inherited by another that's still
// stuck to the old account.
type switchTracker struct {
	mu      sync.Mutex
	entries map[string]switchEntry
	// Test hook — leave nil in production.
	now func() time.Time
}

type switchEntry struct {
	authID   string
	lastSeen time.Time
}

// switchTrackerIdleTTL drops conversations untouched longer than this.
// 2 hours covers normal idle gaps in a session without leaking memory
// for one-shot clients that never come back.
const switchTrackerIdleTTL = 2 * time.Hour

func newSwitchTracker() *switchTracker {
	t := &switchTracker{entries: make(map[string]switchEntry)}
	go t.gcLoop()
	return t
}

// Check records that this conversation is now on currentAuthID and
// returns whether the prior observation (if any) used a different auth.
// First-touch returns false (no prior thinking blocks possible).
//
// Empty inputs are treated as "no signal" — return false, do nothing.
func (t *switchTracker) Check(clientToken string, body []byte, currentAuthID string) bool {
	if clientToken == "" || currentAuthID == "" {
		return false
	}
	convKey := conversationKey(body)
	if convKey == "" {
		return false
	}
	key := clientToken + "|" + convKey

	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.timeNow()
	prev, exists := t.entries[key]
	t.entries[key] = switchEntry{authID: currentAuthID, lastSeen: now}
	return exists && prev.authID != currentAuthID
}

func (t *switchTracker) timeNow() time.Time {
	if t.now != nil {
		return t.now()
	}
	return time.Now()
}

func (t *switchTracker) gcLoop() {
	ticker := time.NewTicker(15 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-switchTrackerIdleTTL)
		t.mu.Lock()
		for k, v := range t.entries {
			if v.lastSeen.Before(cutoff) {
				delete(t.entries, k)
			}
		}
		t.mu.Unlock()
	}
}

// conversationKey hashes the first user message so multi-turn requests
// of the same conversation share one key. Mirrors the anchor SessionIDFor
// uses; kept as a small helper here rather than reaching into mimicry.go's
// extractFirstUserText so this file stays self-contained for testing.
func conversationKey(body []byte) string {
	first := extractFirstUserText(body)
	if first == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(first))
	return hex.EncodeToString(sum[:])[:16]
}
