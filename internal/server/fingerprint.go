package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// Header values pinned to Claude Code 2.1.92 / @anthropic-ai/sdk 0.70.0.
// Aligned with sub2api (DefaultHeaders) and Parrot (cc_mimicry.py) — the
// reference implementations whose values come from real CLI 2.1.9x captures.
// CLICurrentVersion below MUST match the version baked into claudeCLIUserAgent;
// any drift will cause the cc_version=X.Y.Z.{fp} billing block to disagree
// with the User-Agent and trigger Anthropic's third-party detection.
const (
	CLICurrentVersion       = "2.1.92"
	claudeCLIUserAgent      = "claude-cli/2.1.92 (external, cli)"
	claudeStainlessLang     = "js"
	claudeStainlessRuntime  = "node"
	claudeStainlessRuntimeV = "v24.13.0"
	claudeStainlessPackageV = "0.70.0"
	claudeStainlessOS       = "Linux"
	claudeStainlessArch     = "arm64"
	claudeStainlessTimeout  = "600"
	claudeStainlessRetryCnt = "0"
	claudeAnthropicVersion  = "2023-06-01"
	// Beta list mirrors sub2api FullClaudeCodeMimicryBetas() — order matters,
	// upstream uses the full set as a cheap third-party signal. Any beta we
	// drop that real CLI sends will downgrade us to "extra usage" billing.
	claudeAnthropicBetaFull = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,prompt-caching-scope-2026-01-05,effort-2025-11-24,redact-thinking-2026-02-12,context-management-2025-06-27,extended-cache-ttl-2025-04-11"
)

// Default cache_control TTL for cache breakpoints we inject. Real CLI uses
// "1h"; we follow sub2api's "5m" policy to avoid burning 1h cache slots when
// the client didn't ask for them.
const claudeDefaultCacheTTL = "5m"

// Claude Code system prompt — first system block on every real CLI request.
const claudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."

// claudeCodePromptPrefixes detects requests whose system field already looks
// like a Claude Code request — we leave those alone (don't double-inject).
// Mirrors sub2api/internal/service/gateway_service.go.
var claudeCodePromptPrefixes = []string{
	"You are Claude Code, Anthropic's official CLI for Claude",
	"You are a Claude agent, built on Anthropic's Claude Agent SDK",
	"You are a file search specialist for Claude Code",
	"You are a helpful AI assistant tasked with summarizing conversations",
}

// sessionIDCache assigns one stable UUID per credential ID. Matches the
// X-Claude-Code-Session-Id behavior of the real Claude Code CLI, which keeps
// the value steady across requests for the lifetime of a login.
var (
	sessionIDCacheMu sync.Mutex
	sessionIDCache   = make(map[string]string)
)

func sessionIDFor(authID string) string {
	sessionIDCacheMu.Lock()
	defer sessionIDCacheMu.Unlock()
	if v, ok := sessionIDCache[authID]; ok {
		return v
	}
	// Derive deterministically from the auth ID so reloads produce the same
	// value the upstream has previously seen, then format as a UUID v4 shape
	// for cosmetic similarity to the real client.
	sum := sha256.Sum256([]byte("cpa-claude-session/" + authID))
	id := uuidFromBytes(sum[:16])
	sessionIDCache[authID] = id
	return id
}

func newRequestUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is essentially impossible; fall back to a
		// deterministic string so the request still ships.
		return "00000000-0000-4000-8000-000000000000"
	}
	return uuidFromBytes(b[:])
}

func uuidFromBytes(b []byte) string {
	out := make([]byte, 16)
	copy(out, b)
	out[6] = (out[6] & 0x0f) | 0x40 // version 4
	out[8] = (out[8] & 0x3f) | 0x80 // variant RFC 4122
	hexs := hex.EncodeToString(out)
	return fmt.Sprintf("%s-%s-%s-%s-%s", hexs[0:8], hexs[8:12], hexs[12:16], hexs[16:20], hexs[20:32])
}

// ensureHeader sets name=value only if the header isn't already set. Mirrors
// upstream CLIProxyAPI's misc.EnsureHeader so client-supplied values (already
// copied in by copyForwardableHeaders) win over our defaults.
func ensureHeader(h http.Header, name, value string) {
	if strings.TrimSpace(h.Get(name)) == "" {
		h.Set(name, value)
	}
}
