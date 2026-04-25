package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cespare/xxhash/v2"
)

// Body-layer Claude Code mimicry. Header-only spoofing (proxy.go) is enough
// to pass UA / X-Stainless / Anthropic-Beta inspection, but Anthropic also
// inspects the request body for signals that the official CLI always emits:
//
//  1. system[0] is an "x-anthropic-billing-header" text block carrying
//     cc_version=X.Y.Z.{3hex}, cc_entrypoint=cli, and cch={5hex}
//     (xxhash64 of the body with a fixed seed).
//  2. system[1] is "You are Claude Code, Anthropic's official CLI for Claude."
//     with cache_control: ephemeral.
//  3. messages carry a stable cache breakpoint on the last block + (optionally)
//     the second-to-last user turn.
//  4. metadata.user_id is JSON: {"device_id":..., "account_uuid":..., "session_id":...}
//
// Missing any of these downgrades the request to "third-party app" billing on
// OAuth credentials. The algorithms below are reverse-engineered constants
// (fingerprintSalt, cchSeed) from sub2api / Parrot — keeping them byte-for-byte
// identical is what lets us look like the real CLI.

const (
	// fingerprintSalt is the salt used in the cc_version 3-char fingerprint.
	// Originated from a real CLI capture; do not change.
	fingerprintSalt = "59cf53e54c78"
	// cchSeed is the xxhash64 seed for the billing header cch field.
	cchSeed uint64 = 0x6E52736AC806831E
)

var cchPlaceholderRe = regexp.MustCompile(`(x-anthropic-billing-header:[^"]*?\bcch=)(00000)(;)`)

// applyClaudeCodeBodyMimicry rewrites the JSON request body to match the
// shape of a real Claude Code CLI request. Returns the original body
// unchanged if any step fails (best-effort — the request still ships).
//
// authID seeds a stable device_id per credential so multi-turn conversations
// keep the same metadata.user_id across requests.
//
// Skips entirely when:
//   - body isn't a JSON object (not an Anthropic /v1/messages payload)
//   - model contains "haiku" (Anthropic doesn't third-party-check Haiku)
//   - the request already looks like Claude Code (system already has the
//     official prompt prefix — likely a real CLI client passing through)
func applyClaudeCodeBodyMimicry(body []byte, model, authID string) []byte {
	if len(body) == 0 || strings.Contains(strings.ToLower(model), "haiku") {
		return body
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}

	// Detect "already a Claude Code request" — leave it alone, double-injection
	// would corrupt the system field.
	if hasClaudeCodeSystemPrefix(obj["system"]) {
		// Even if system already looks right, refresh the cch signature so it
		// matches the body we're actually about to send.
		return signBillingHeaderCCH(body)
	}

	// Step 1: rewrite system → [billing_block, claude_code_block], move
	// original system into messages as user/assistant pair.
	out, err := rewriteSystemForOAuth(obj, body)
	if err != nil {
		return body
	}

	// Step 2: stable cache breakpoints on messages.
	out = stripMessageCacheControl(out)
	out = addMessageCacheBreakpoints(out)

	// Step 3: metadata.user_id (JSON shape, CLI 2.1.78+).
	out = ensureMetadataUserID(out, authID)

	// Step 4: replace cch=00000 placeholder with xxhash64 of the final body.
	// MUST be the last transformation — any later edit invalidates the hash.
	out = signBillingHeaderCCH(out)
	return out
}

// hasClaudeCodeSystemPrefix reports whether the system field already contains
// one of the Claude Code prompt prefixes. Accepts string, []block, or null.
func hasClaudeCodeSystemPrefix(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return matchesClaudeCodePrefix(asString)
	}
	var asArray []map[string]any
	if err := json.Unmarshal(raw, &asArray); err == nil {
		for _, blk := range asArray {
			if t, _ := blk["text"].(string); matchesClaudeCodePrefix(t) {
				return true
			}
		}
	}
	return false
}

func matchesClaudeCodePrefix(text string) bool {
	for _, p := range claudeCodePromptPrefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// extractSystemText returns the original system content as plain text so we
// can move it into messages. Concatenates text blocks for array form.
func extractSystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}
	var asArray []map[string]any
	if err := json.Unmarshal(raw, &asArray); err == nil {
		var parts []string
		for _, blk := range asArray {
			if t, _ := blk["text"].(string); strings.TrimSpace(t) != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}

// rewriteSystemForOAuth replaces system with the canonical 2-block Claude
// Code shape and prepends the original system content to messages as a
// user/assistant pair.
func rewriteSystemForOAuth(obj map[string]json.RawMessage, body []byte) ([]byte, error) {
	originalSystem := extractSystemText(obj["system"])

	billing := buildBillingBlock(body, CLICurrentVersion)
	ccBlock := buildSystemTextBlock(claudeCodeSystemPrompt, true)

	systemArr, err := json.Marshal([]json.RawMessage{billing, ccBlock})
	if err != nil {
		return nil, err
	}
	obj["system"] = systemArr

	if originalSystem != "" && !matchesClaudeCodePrefix(originalSystem) {
		// Inject original system as [user, assistant] pair at the head of
		// messages so the model still receives the instructions.
		instr, _ := json.Marshal(map[string]any{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "[System Instructions]\n" + originalSystem},
			},
		})
		ack, _ := json.Marshal(map[string]any{
			"role": "assistant",
			"content": []map[string]any{
				{"type": "text", "text": "Understood. I will follow these instructions."},
			},
		})

		var existing []json.RawMessage
		if raw, ok := obj["messages"]; ok && len(raw) > 0 {
			_ = json.Unmarshal(raw, &existing)
		}
		merged := append([]json.RawMessage{instr, ack}, existing...)
		if mb, err := json.Marshal(merged); err == nil {
			obj["messages"] = mb
		}
	}

	return json.Marshal(obj)
}

// buildSystemTextBlock returns a marshalled {"type":"text","text":...,
// "cache_control":{"type":"ephemeral","ttl":"5m"}} block.
func buildSystemTextBlock(text string, cache bool) json.RawMessage {
	if cache {
		out, _ := json.Marshal(map[string]any{
			"type": "text",
			"text": text,
			"cache_control": map[string]any{
				"type": "ephemeral",
				"ttl":  claudeDefaultCacheTTL,
			},
		})
		return out
	}
	out, _ := json.Marshal(map[string]any{"type": "text", "text": text})
	return out
}

// buildBillingBlock returns the system[0] x-anthropic-billing-header block.
// cch=00000 is a placeholder; signBillingHeaderCCH replaces it after the
// rest of the body is finalized (so the hash covers the final bytes).
func buildBillingBlock(body []byte, cliVersion string) json.RawMessage {
	fp := computeClaudeCodeFingerprint(body, cliVersion)
	text := fmt.Sprintf("x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=cli; cch=00000;", cliVersion, fp)
	out, _ := json.Marshal(map[string]any{"type": "text", "text": text})
	return out
}

// computeClaudeCodeFingerprint replicates the real CLI's cc_version suffix:
//
//  1. Take the first text in the first user message.
//  2. Pick characters at positions 4, 7, 20 (pad with '0' if shorter).
//  3. SHA256(salt + chars + cliVersion); take hex[:3].
//
// Algorithm and constants must match Parrot src/transform/cc_mimicry.py
// byte-for-byte; the upstream uses this triplet to detect non-CLI clients.
func computeClaudeCodeFingerprint(body []byte, version string) string {
	first := extractFirstUserText(body)
	chars := make([]byte, 0, 3)
	for _, idx := range []int{4, 7, 20} {
		if idx < len(first) {
			chars = append(chars, first[idx])
		} else {
			chars = append(chars, '0')
		}
	}
	sum := sha256.Sum256([]byte(fingerprintSalt + string(chars) + version))
	return hex.EncodeToString(sum[:])[:3]
}

func extractFirstUserText(body []byte) string {
	var obj struct {
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &obj); err != nil {
		return ""
	}
	for _, m := range obj.Messages {
		if m.Role != "user" {
			continue
		}
		// content can be string or []block
		var asString string
		if err := json.Unmarshal(m.Content, &asString); err == nil {
			return asString
		}
		var blocks []map[string]any
		if err := json.Unmarshal(m.Content, &blocks); err == nil {
			for _, b := range blocks {
				if t, _ := b["type"].(string); t == "text" {
					if s, _ := b["text"].(string); s != "" {
						return s
					}
				}
			}
		}
		return ""
	}
	return ""
}

// signBillingHeaderCCH replaces the cch=00000 placeholder in the billing
// block with a 5-char hex digest of the body. xxhash64-with-seed matches
// what the real CLI uses (signature derived from Parrot reverse-engineering).
func signBillingHeaderCCH(body []byte) []byte {
	if !cchPlaceholderRe.Match(body) {
		return body
	}
	d := xxhash.NewWithSeed(cchSeed)
	_, _ = d.Write(body)
	cch := fmt.Sprintf("%05x", d.Sum64()&0xFFFFF)
	return cchPlaceholderRe.ReplaceAll(body, []byte("${1}"+cch+"${3}"))
}

// stripMessageCacheControl removes any cache_control block clients put on
// messages[*].content[*]. Real CLI rewrites breakpoints every turn rather
// than letting the client persist them — leaving stale ones causes prefix
// drift and breaks cache hits across turns.
func stripMessageCacheControl(body []byte) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	rawMsgs, ok := obj["messages"]
	if !ok {
		return body
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(rawMsgs, &msgs); err != nil {
		return body
	}
	changed := false
	for _, m := range msgs {
		contentRaw, ok := m["content"]
		if !ok {
			continue
		}
		var blocks []map[string]json.RawMessage
		if err := json.Unmarshal(contentRaw, &blocks); err != nil {
			continue
		}
		blockChanged := false
		for _, b := range blocks {
			if _, ok := b["cache_control"]; ok {
				delete(b, "cache_control")
				blockChanged = true
				changed = true
			}
		}
		if blockChanged {
			if nb, err := json.Marshal(blocks); err == nil {
				m["content"] = nb
			}
		}
	}
	if !changed {
		return body
	}
	if nm, err := json.Marshal(msgs); err == nil {
		obj["messages"] = nm
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// addMessageCacheBreakpoints injects up to two ephemeral cache_control
// blocks on:
//  1. The last message (always, when there is at least one).
//  2. The second-to-last user message (only when len(messages) >= 4).
//
// Mirrors sub2api/Parrot exactly — those positions are where the real CLI
// places its breakpoints and matching them is part of the prefix-cache
// stability story.
func addMessageCacheBreakpoints(body []byte) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}
	rawMsgs, ok := obj["messages"]
	if !ok {
		return body
	}
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(rawMsgs, &msgs); err != nil {
		return body
	}
	if len(msgs) == 0 {
		return body
	}

	injectAt := func(i int) {
		if i < 0 || i >= len(msgs) {
			return
		}
		msgs[i] = injectBreakpointOnMessage(msgs[i])
	}

	injectAt(len(msgs) - 1)

	if len(msgs) >= 4 {
		userCount := 0
		for i := len(msgs) - 1; i >= 0; i-- {
			var role string
			if r, ok := msgs[i]["role"]; ok {
				_ = json.Unmarshal(r, &role)
			}
			if role != "user" {
				continue
			}
			userCount++
			if userCount == 2 {
				injectAt(i)
				break
			}
		}
	}

	if nm, err := json.Marshal(msgs); err == nil {
		obj["messages"] = nm
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return out
}

// injectBreakpointOnMessage adds cache_control to the last content block of
// one message. If content is a plain string, upgrades it to a single-element
// text-block array first.
func injectBreakpointOnMessage(msg map[string]json.RawMessage) map[string]json.RawMessage {
	contentRaw, ok := msg["content"]
	if !ok {
		return msg
	}

	// String form → upgrade to single-block array carrying the breakpoint.
	var asString string
	if err := json.Unmarshal(contentRaw, &asString); err == nil {
		blk, _ := json.Marshal(map[string]any{
			"type": "text",
			"text": asString,
			"cache_control": map[string]any{
				"type": "ephemeral",
				"ttl":  claudeDefaultCacheTTL,
			},
		})
		arr, _ := json.Marshal([]json.RawMessage{blk})
		msg["content"] = arr
		return msg
	}

	var blocks []map[string]any
	if err := json.Unmarshal(contentRaw, &blocks); err != nil {
		return msg
	}
	if len(blocks) == 0 {
		return msg
	}
	last := blocks[len(blocks)-1]
	if cc, ok := last["cache_control"].(map[string]any); ok {
		if ttl, _ := cc["ttl"].(string); ttl != "" {
			return msg // client already set a breakpoint here — respect it
		}
		cc["ttl"] = claudeDefaultCacheTTL
		last["cache_control"] = cc
	} else {
		last["cache_control"] = map[string]any{
			"type": "ephemeral",
			"ttl":  claudeDefaultCacheTTL,
		}
	}
	blocks[len(blocks)-1] = last
	if nb, err := json.Marshal(blocks); err == nil {
		msg["content"] = nb
	}
	return msg
}

// ensureMetadataUserID writes a JSON-shaped metadata.user_id (the format
// used by CLI >= 2.1.78). device_id is derived deterministically from
// authID so the same OAuth credential always produces the same id;
// session_id is derived from the body so it stays stable for retries of
// the exact same request.
func ensureMetadataUserID(body []byte, authID string) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}

	// Don't overwrite a user-supplied metadata.user_id.
	if rawMD, ok := obj["metadata"]; ok && len(rawMD) > 0 {
		var md map[string]json.RawMessage
		if err := json.Unmarshal(rawMD, &md); err == nil {
			if uid, ok := md["user_id"]; ok && len(uid) > 0 && string(uid) != "null" && string(uid) != `""` {
				return body
			}
			deviceID := deviceIDFor(authID)
			sessionID := sessionUUIDFor(authID, body)
			uid := buildJSONUserID(deviceID, "", sessionID)
			md["user_id"], _ = json.Marshal(uid)
			if nm, err := json.Marshal(md); err == nil {
				obj["metadata"] = nm
				out, _ := json.Marshal(obj)
				return out
			}
			return body
		}
	}

	deviceID := deviceIDFor(authID)
	sessionID := sessionUUIDFor(authID, body)
	uid := buildJSONUserID(deviceID, "", sessionID)
	md, _ := json.Marshal(map[string]any{"user_id": uid})
	obj["metadata"] = md
	out, _ := json.Marshal(obj)
	return out
}

// buildJSONUserID returns the JSON-form user_id used by CLI 2.1.78+:
//
//	{"device_id":"...", "account_uuid":"...", "session_id":"..."}
//
// account_uuid is allowed to be empty when we don't know it (we never do
// — we don't talk to Anthropic's account API).
func buildJSONUserID(deviceID, accountUUID, sessionID string) string {
	b, _ := json.Marshal(map[string]string{
		"device_id":    deviceID,
		"account_uuid": accountUUID,
		"session_id":   sessionID,
	})
	return string(b)
}

// deviceIDFor maps an authID to a stable 64-char hex device id, so the
// same credential always shows the same device_id to upstream.
func deviceIDFor(authID string) string {
	sum := sha256.Sum256([]byte("cpa-claude-device/" + authID))
	return hex.EncodeToString(sum[:])
}

// sessionUUIDFor derives a deterministic UUIDv4-shaped session id from
// (authID, body-hash). Re-running the exact same body keeps the same
// session — multi-turn conversations naturally rotate as the body grows.
func sessionUUIDFor(authID string, body []byte) string {
	h := sha256.New()
	h.Write([]byte("cpa-claude-session/"))
	h.Write([]byte(authID))
	h.Write([]byte("|"))
	h.Write(body)
	sum := h.Sum(nil)
	return uuidFromBytes(sum[:16])
}
