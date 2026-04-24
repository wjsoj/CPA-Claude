package auth

import "strings"

// Upstream provider identifiers. Every Auth carries one so the pool, pricing,
// request log, and admin UI can route/aggregate per provider. Empty string
// from legacy data (written before multi-provider support) is treated as
// Anthropic by NormalizeProvider — that's where all pre-existing creds came
// from.
const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
)

// NormalizeProvider canonicalizes a provider name. Accepts a few common
// aliases (claude → anthropic, codex/chatgpt → openai) so legacy configs and
// admin UI inputs don't need to match exactly. Unknown non-empty values are
// preserved case-folded, but the rest of the system only understands the two
// canonical values above.
func NormalizeProvider(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "anthropic", "claude":
		return ProviderAnthropic
	case "openai", "codex", "chatgpt":
		return ProviderOpenAI
	}
	return s
}

// IsKnownProvider reports whether p is one of the canonical provider ids the
// rest of the system routes on.
func IsKnownProvider(p string) bool {
	p = NormalizeProvider(p)
	return p == ProviderAnthropic || p == ProviderOpenAI
}
