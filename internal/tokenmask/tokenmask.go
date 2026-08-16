// Package tokenmask owns the one masking form a client token takes when it
// leaves the server: in request-log rows, in the admin API, and in the team
// console's member identifiers.
//
// It exists because the mask is a *join key*, not decoration. The request log
// stores only the masked token, so any query that wants one client's rows has
// to reproduce this function byte for byte — and a mismatch does not error, it
// returns an empty result set. Four copies of this code used to live in
// internal/server (two: the proxy's write side and the billing path's log
// lines), internal/admin and internal/saas/billing; they agreed, but nothing
// was pinning them. All four now forward here, including the log-line-only one
// — a copy that is harmless today is how the drift starts.
//
// Beware cc-core's auth.MaskToken: same idea, different bytes (4-char prefix
// and three ASCII dots). It masks credential IDs for operator logs and must not
// be substituted here.
package tokenmask

// Mask renders tok as a 6-byte prefix, U+2026 HORIZONTAL ELLIPSIS, and a
// 4-byte suffix. Tokens too short to leave a meaningful gap collapse to "***",
// which is therefore NOT a usable identity — callers that key on the mask must
// treat it as "cannot distinguish this token".
func Mask(tok string) string {
	if len(tok) <= 10 {
		return Opaque
	}
	return tok[:6] + "…" + tok[len(tok)-4:]
}

// Opaque is what Mask returns for a token short enough that prefix and suffix
// would overlap. Every such token shares this one string, so usage keyed on the
// mask must report them as unmeasurable rather than pooling them together.
const Opaque = "***"
