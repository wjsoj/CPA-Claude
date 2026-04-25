package server

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMimicrySmoke exercises the body rewriter on the shapes /v1/messages
// requests actually take. It doesn't assert byte-equality against a golden
// payload (the cch hash and session UUID change with body content) — it
// asserts the structural invariants real Claude Code requests carry.
func TestMimicrySmoke(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{
			name: "string-system",
			in:   `{"model":"claude-sonnet-4-5","system":"You are a helpful assistant.","messages":[{"role":"user","content":"hello"}]}`,
		},
		{
			name: "array-system",
			in:   `{"model":"claude-sonnet-4-5","system":[{"type":"text","text":"You are a helpful assistant."}],"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`,
		},
		{
			name: "no-system",
			in:   `{"model":"claude-opus-4-5","messages":[{"role":"user","content":"hi"}]}`,
		},
		{
			name: "multi-turn",
			in:   `{"model":"claude-sonnet-4-5","system":"sys","messages":[{"role":"user","content":"q1"},{"role":"assistant","content":"a1"},{"role":"user","content":"q2"},{"role":"assistant","content":"a2"},{"role":"user","content":"q3"}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := applyClaudeCodeBodyMimicry([]byte(tc.in), "claude-sonnet-4-5", "test-auth-id")

			// 1. Output must still be valid JSON.
			var parsed map[string]json.RawMessage
			if err := json.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("output not valid JSON: %v\n%s", err, out)
			}

			// 2. system must be a 2-element array: [billing, claude-code-prompt].
			var sys []map[string]any
			if err := json.Unmarshal(parsed["system"], &sys); err != nil {
				t.Fatalf("system not array: %v", err)
			}
			if len(sys) != 2 {
				t.Fatalf("expected 2 system blocks, got %d", len(sys))
			}
			billing, _ := sys[0]["text"].(string)
			if !strings.HasPrefix(billing, "x-anthropic-billing-header:") {
				t.Errorf("system[0] missing billing prefix: %q", billing)
			}
			if !strings.Contains(billing, "cc_version=2.1.92.") {
				t.Errorf("system[0] missing cc_version: %q", billing)
			}
			// cch must have been signed (no longer 00000).
			if strings.Contains(billing, "cch=00000") {
				t.Errorf("system[0] cch placeholder still present: %q", billing)
			}
			ccPrompt, _ := sys[1]["text"].(string)
			if !strings.HasPrefix(ccPrompt, "You are Claude Code") {
				t.Errorf("system[1] missing CC prompt: %q", ccPrompt)
			}
			if _, hasCC := sys[1]["cache_control"]; !hasCC {
				t.Errorf("system[1] missing cache_control")
			}

			// 3. metadata.user_id must be present and JSON-shaped.
			var md map[string]json.RawMessage
			if err := json.Unmarshal(parsed["metadata"], &md); err != nil {
				t.Fatalf("metadata not object: %v", err)
			}
			var uidStr string
			if err := json.Unmarshal(md["user_id"], &uidStr); err != nil {
				t.Fatalf("metadata.user_id not string: %v", err)
			}
			var uidObj map[string]string
			if err := json.Unmarshal([]byte(uidStr), &uidObj); err != nil {
				t.Fatalf("metadata.user_id not JSON-encoded: %v (%q)", err, uidStr)
			}
			if len(uidObj["device_id"]) != 64 {
				t.Errorf("device_id wrong length: %d", len(uidObj["device_id"]))
			}
			if uidObj["session_id"] == "" {
				t.Errorf("session_id empty")
			}

			// 4. Last message has cache_control on its last content block.
			var msgs []map[string]json.RawMessage
			if err := json.Unmarshal(parsed["messages"], &msgs); err != nil {
				t.Fatalf("messages not array: %v", err)
			}
			if len(msgs) == 0 {
				t.Fatalf("messages empty after rewrite")
			}
			last := msgs[len(msgs)-1]
			var lastBlocks []map[string]any
			if err := json.Unmarshal(last["content"], &lastBlocks); err != nil {
				t.Fatalf("last message content not array: %v", err)
			}
			if len(lastBlocks) == 0 {
				t.Fatalf("last message has no content blocks")
			}
			if _, ok := lastBlocks[len(lastBlocks)-1]["cache_control"]; !ok {
				t.Errorf("last content block missing cache_control")
			}
		})
	}
}

// TestHaikuSkip confirms Haiku models bypass body rewriting entirely
// (Anthropic doesn't third-party-check Haiku).
func TestHaikuSkip(t *testing.T) {
	in := `{"model":"claude-haiku-4-5","system":"sys","messages":[{"role":"user","content":"hi"}]}`
	out := applyClaudeCodeBodyMimicry([]byte(in), "claude-haiku-4-5-20251001", "auth-x")
	if string(out) != in {
		t.Errorf("haiku body was modified — should be passthrough\nin=%s\nout=%s", in, out)
	}
}

// TestAlreadyClaudeCode confirms we don't double-rewrite a request whose
// system already starts with the Claude Code prompt.
func TestAlreadyClaudeCode(t *testing.T) {
	in := `{"model":"claude-sonnet-4-5","system":[{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude. Stuff."}],"messages":[{"role":"user","content":"hi"}]}`
	out := applyClaudeCodeBodyMimicry([]byte(in), "claude-sonnet-4-5", "auth-x")

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("invalid output JSON: %v", err)
	}
	var sys []map[string]any
	if err := json.Unmarshal(parsed["system"], &sys); err != nil {
		t.Fatalf("system not array: %v", err)
	}
	// Should be left as 1-element (we didn't expand into 2-block form).
	if len(sys) != 1 {
		t.Errorf("already-CC system should not be rewritten, got %d blocks", len(sys))
	}
}

// TestCCHSigning asserts the cch field is replaced with a 5-hex digest
// derived from body content (different bodies → different cch).
func TestCCHSigning(t *testing.T) {
	a := applyClaudeCodeBodyMimicry(
		[]byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"alpha"}]}`),
		"claude-sonnet-4-5", "auth-1",
	)
	b := applyClaudeCodeBodyMimicry(
		[]byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"beta"}]}`),
		"claude-sonnet-4-5", "auth-1",
	)
	if string(a) == string(b) {
		t.Fatalf("two distinct bodies produced identical output — cch not signing body content")
	}
}
