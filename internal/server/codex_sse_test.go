package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/usage"
)

// errReader fails its first Read, simulating an upstream that resets the
// connection after the 200 but before any SSE event arrives.
type errReader struct{ err error }

func (e *errReader) Read(p []byte) (int, error) { return 0, e.err }

// A stream that breaks before emitting any event must NOT commit the response
// headers, so forwardWithFailover can still transparently retry on another
// credential. This is the core of the "stream disconnected before completion"
// fix.
func TestStreamSSECodexBackendNoCommitBeforeFirstByte(t *testing.T) {
	c, w := newCodexStreamCtx()
	resp := &http.Response{Body: io.NopCloser(&errReader{err: errors.New("connection reset by peer")})}

	committed := false
	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() { committed = true })

	if committed {
		t.Error("commit() must not run when the stream breaks before any byte (else retry is impossible)")
	}
	if res.wroteAny {
		t.Error("wroteAny must be false when nothing reached the client")
	}
	if res.sawTerminal {
		t.Error("sawTerminal must be false")
	}
	if res.err == nil {
		t.Error("err must be set so the caller knows to retry")
	}
	if w.Body.Len() != 0 {
		t.Errorf("nothing should be written to the client, got: %q", w.Body.String())
	}
}

func TestCodexTerminalEvent(t *testing.T) {
	terminal := []string{
		`{"type":"response.completed","response":{"usage":{}}}`,
		`{"type":"response.failed"}`,
		`{"type":"response.incomplete"}`,
		`{"type":"response.cancelled"}`,
		`{"type":"response.canceled"}`,
	}
	for _, p := range terminal {
		if !codexTerminalEvent([]byte(p)) {
			t.Errorf("expected terminal event for %s", p)
		}
	}
	nonTerminal := []string{
		`{"type":"response.output_item.done"}`,
		`{"type":"response.output_text.delta","delta":"hi"}`,
		`{"type":"response.created"}`,
		`not json`,
		``,
	}
	for _, p := range nonTerminal {
		if codexTerminalEvent([]byte(p)) {
			t.Errorf("did not expect terminal event for %q", p)
		}
	}
}

func newCodexStreamCtx() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

// A stream that EOFs without a terminal event is reported as truncated, but the
// bytes already received are still passed through to the client verbatim.
func TestStreamSSECodexBackendTruncated(t *testing.T) {
	body := "data: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	c, w := newCodexStreamCtx()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() {})
	if res.sawTerminal {
		t.Error("stream without a terminal event should report sawTerminal=false")
	}
	if !res.wroteAny {
		t.Error("partial bytes were relayed, so wroteAny must be true")
	}
	if res.err == nil {
		t.Error("a truncated stream must report a non-nil err")
	}
	if !strings.Contains(w.Body.String(), "response.output_text.delta") {
		t.Errorf("partial bytes must still reach the client, got: %q", w.Body.String())
	}
}

// A stream ending with response.completed is reported complete and forwarded
// verbatim.
func TestStreamSSECodexBackendCompleted(t *testing.T) {
	body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":5}}}\n\n"
	c, w := newCodexStreamCtx()
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() {})
	if !res.sawTerminal {
		t.Error("stream ending in response.completed should report sawTerminal=true")
	}
	if res.err != nil {
		t.Errorf("a cleanly terminated stream must report err=nil, got: %v", res.err)
	}
	if !strings.Contains(w.Body.String(), "response.completed") {
		t.Errorf("terminal event must reach the client, got: %q", w.Body.String())
	}
	if counts.OutputTokens != 5 || counts.InputTokens != 10 {
		t.Errorf("usage must be extracted from response.completed, got in=%d out=%d", counts.InputTokens, counts.OutputTokens)
	}
}
