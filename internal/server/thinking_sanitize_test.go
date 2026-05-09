package server

import (
	"encoding/json"
	"testing"
)

func TestSanitizeThinkingForSwitch(t *testing.T) {
	tests := []struct {
		name string
		in   string
		// fn checks the parsed result. Return error string on failure
		// or "" on success.
		check func(out []byte) string
	}{
		{
			name: "drops thinking block from past assistant",
			in: `{
				"messages":[
					{"role":"user","content":"hi"},
					{"role":"assistant","content":[
						{"type":"thinking","thinking":"reasoning","signature":"sig_A_xxx"},
						{"type":"text","text":"hello"}
					]},
					{"role":"user","content":"more"}
				]
			}`,
			check: func(out []byte) string {
				var obj struct {
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(out, &obj); err != nil {
					return "parse: " + err.Error()
				}
				if len(obj.Messages) != 3 {
					return "expected 3 messages"
				}
				var blocks []map[string]any
				if err := json.Unmarshal(obj.Messages[1].Content, &blocks); err != nil {
					return "assistant content not array: " + err.Error()
				}
				if len(blocks) != 1 || blocks[0]["type"] != "text" {
					return "expected only the text block to remain"
				}
				return ""
			},
		},
		{
			name: "strips signature from tool_use block",
			in: `{
				"messages":[
					{"role":"assistant","content":[
						{"type":"tool_use","id":"toolu_1","name":"Read","input":{},"signature":"injected"}
					]}
				]
			}`,
			check: func(out []byte) string {
				var obj struct {
					Messages []struct {
						Content []map[string]any `json:"content"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(out, &obj); err != nil {
					return "parse: " + err.Error()
				}
				blk := obj.Messages[0].Content[0]
				if _, has := blk["signature"]; has {
					return "signature should be removed from tool_use"
				}
				if blk["type"] != "tool_use" || blk["name"] != "Read" {
					return "tool_use fields lost"
				}
				return ""
			},
		},
		{
			name: "user thinking blocks are never touched",
			in: `{
				"messages":[
					{"role":"user","content":[
						{"type":"thinking","thinking":"x","signature":"y"}
					]}
				]
			}`,
			check: func(out []byte) string {
				var obj struct {
					Messages []struct {
						Content []map[string]any `json:"content"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(out, &obj); err != nil {
					return "parse: " + err.Error()
				}
				if len(obj.Messages[0].Content) != 1 {
					return "user content modified"
				}
				return ""
			},
		},
		{
			name: "string-content assistant is left untouched",
			in: `{
				"messages":[
					{"role":"assistant","content":"hi"}
				]
			}`,
			check: func(out []byte) string {
				var obj struct {
					Messages []struct {
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(out, &obj); err != nil {
					return "parse: " + err.Error()
				}
				var s string
				if err := json.Unmarshal(obj.Messages[0].Content, &s); err != nil {
					return "content type changed"
				}
				if s != "hi" {
					return "content text changed"
				}
				return ""
			},
		},
		{
			name: "no-op preserves bytes for unaffected request",
			in: `{
				"messages":[
					{"role":"user","content":"hi"},
					{"role":"assistant","content":[
						{"type":"text","text":"hello"}
					]}
				]
			}`,
			check: func(out []byte) string {
				// Round-trip is fine; the important check is the body
				// still decodes and assistant content blocks are intact.
				var obj struct {
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(out, &obj); err != nil {
					return "parse: " + err.Error()
				}
				if len(obj.Messages) != 2 {
					return "expected 2 messages"
				}
				var blocks []map[string]any
				if err := json.Unmarshal(obj.Messages[1].Content, &blocks); err != nil {
					return "assistant content not array: " + err.Error()
				}
				if len(blocks) != 1 || blocks[0]["type"] != "text" {
					return "assistant content changed"
				}
				return ""
			},
		},
		{
			name: "malformed body returns input unchanged",
			in:   `not json`,
			check: func(out []byte) string {
				if string(out) != "not json" {
					return "should be passthrough"
				}
				return ""
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := sanitizeThinkingForSwitch([]byte(tt.in))
			if msg := tt.check(out); msg != "" {
				t.Errorf("%s: %s\noutput=%s", tt.name, msg, string(out))
			}
		})
	}
}

func TestSwitchTrackerDetection(t *testing.T) {
	tr := &switchTracker{entries: make(map[string]switchEntry)}
	body := []byte(`{"messages":[{"role":"user","content":"hi"}]}`)

	if tr.Check("ct1", body, "auth-A") {
		t.Fatal("first touch should not signal a switch")
	}
	if tr.Check("ct1", body, "auth-A") {
		t.Fatal("same auth should not signal a switch")
	}
	if !tr.Check("ct1", body, "auth-B") {
		t.Fatal("changed auth should signal a switch")
	}
	if tr.Check("ct1", body, "auth-B") {
		t.Fatal("re-stable on B should not signal again")
	}

	// Different conversation under same client token is independent.
	body2 := []byte(`{"messages":[{"role":"user","content":"different topic"}]}`)
	if tr.Check("ct1", body2, "auth-A") {
		t.Fatal("a fresh conversation should not signal a switch on first touch")
	}

	// Empty inputs are safe no-signals.
	if tr.Check("", body, "auth-A") {
		t.Fatal("empty clientToken should not signal")
	}
	if tr.Check("ct1", []byte(`{}`), "auth-A") {
		t.Fatal("empty messages should not signal")
	}
}
