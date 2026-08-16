package server

import (
	"testing"

	"github.com/wjsoj/CPA-Claude/internal/tokenmask"
	"github.com/wjsoj/cc-core/auth"
)

// TestMaskClientTokenIsTheQueryKey pins the write side of the request log to
// the same mask every usage query reads with. Nothing else enforces this: a
// query whose mask doesn't match returns zero rows and no error, so a drift
// here shows up as "the team panel says everyone spent nothing" — which is the
// exact bug the group-usage work was written to fix.
//
// The failure mode to guard is a plausible-looking swap to cc-core's
// auth.MaskToken, which masks credential IDs with a 4-byte prefix and ASCII
// dots. Asserted below so the two can never be confused.
func TestMaskClientTokenIsTheQueryKey(t *testing.T) {
	for _, tok := range []string{
		"sk-abcdefghijklmnopqrstuvwxyz",
		"sk-cpa-0123456789abcdef",
		"short",
	} {
		if got, want := maskClientToken(tok), tokenmask.Mask(tok); got != want {
			t.Fatalf("maskClientToken(%q) = %q, want %q", tok, got, want)
		}
	}
	const tok = "sk-abcdefghijklmnopqrstuvwxyz"
	if maskClientToken(tok) == auth.MaskToken(tok) {
		t.Fatalf("client-token mask collapsed onto auth.MaskToken (%q)", auth.MaskToken(tok))
	}
}
