package tokenmask

import (
	"strings"
	"testing"
)

// TestMaskGolden pins the exact bytes. The mask is the join key between the
// request log and every usage query, and a mismatch produces an empty result
// set rather than an error — so the shape has to be nailed down, not merely
// consistent with itself. In particular the separator is U+2026 (0xE2 0x80
// 0xA6), not three ASCII dots, and the prefix is 6 bytes, not 4: cc-core's
// auth.MaskToken uses the other form for credential IDs and swapping them in
// here would silently zero every team's usage.
func TestMaskGolden(t *testing.T) {
	const tok = "sk-abcdefghijklmnopqrstuvwxyz"
	got := Mask(tok)
	if got != "sk-abc…wxyz" {
		t.Fatalf("mask = %q", got)
	}
	want := []byte{'s', 'k', '-', 'a', 'b', 'c', 0xE2, 0x80, 0xA6, 'w', 'x', 'y', 'z'}
	if string(want) != got {
		t.Fatalf("mask bytes = % x, want % x", got, want)
	}
}

func TestMaskShortTokensAreOpaque(t *testing.T) {
	for _, tok := range []string{"", "a", "0123456789"} {
		if got := Mask(tok); got != Opaque {
			t.Fatalf("Mask(%q) = %q, want %q", tok, got, Opaque)
		}
	}
	// 11 bytes is the first length that masks distinguishably.
	if got := Mask("01234567890"); got != "012345…7890" {
		t.Fatalf("Mask(11 chars) = %q", got)
	}
}

// MinDistinguishableLen is the length admin token creation refuses at, and Mask
// is what makes that the right number. Pinning them together keeps the guard
// honest if the 6+4 shape is ever changed: a token one byte longer must mask to
// something that names it, and one at the bound must not.
func TestMinDistinguishableLenMatchesMask(t *testing.T) {
	at := strings.Repeat("a", MinDistinguishableLen)
	if got := Mask(at); got != Opaque {
		t.Fatalf("Mask(%d bytes) = %q, want the opaque form", len(at), got)
	}
	over := at + "b"
	if got := Mask(over); got == Opaque {
		t.Fatalf("Mask(%d bytes) is opaque, so the creation guard lets through a token nothing can key on", len(over))
	}
}
