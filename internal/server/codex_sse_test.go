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

// Capacity shed arrives as an in-band `error` frame followed by
// `response.failed`, inside an otherwise-200 stream. If the error frame is
// forwarded it (a) commits the response, foreclosing failover, and (b) reaches
// the CLI as ApiError::ServerOverloaded, which is terminal for the session
// ("Selected model is at capacity"). Withhold the whole stream instead.
func TestStreamSSECodexBackendShedsCapacityBeforeOutput(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "slow_down", "rate_limit_exceeded"} {
		t.Run(code, func(t *testing.T) {
			c, w := newCodexStreamCtx()
			body := "event: error\n" +
				`data: {"type":"error","error":{"code":"` + code + `","message":"nope"}}` + "\n\n" +
				"event: response.failed\n" +
				`data: {"type":"response.failed","response":{"error":{"code":"` + code + `"}}}` + "\n\n"
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

			committed := false
			var counts usage.Counts
			res := streamSSECodexBackend(c, resp, &counts, func() { committed = true })

			if committed {
				t.Error("commit() must not run — the response must stay uncommitted so failover works")
			}
			if res.wroteAny {
				t.Error("wroteAny must be false so the caller's pre-output failover fires")
			}
			if res.sawTerminal {
				t.Error("sawTerminal must be false — the withheld response.failed must not look like a clean end")
			}
			if res.shed == "" {
				t.Error("shed must carry the withheld frame for diagnostics")
			}
			// A withheld shed is reported through `shed`, never through
			// `demoted` — the caller uses the latter to decide whether to log a
			// turn the client already saw, and a withheld one it never did.
			if res.demoted.shed {
				t.Error("a withheld pre-output shed must not also be reported as demoted")
			}
			if got := w.Body.String(); got != "" {
				t.Errorf("nothing may reach the client, got %q", got)
			}
		})
	}
}

// Once output has started there is no failover left, so the frame must be
// forwarded — but demoted out of the CLI's session-ending code set so it
// retries instead of giving up. The human-readable message must survive.
func TestStreamSSECodexBackendDemotesCapacityAfterOutput(t *testing.T) {
	c, w := newCodexStreamCtx()
	body := "event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n" +
		"event: error\n" +
		`data: {"type":"error","error":{"code":"server_is_overloaded","message":"we are full"}}` + "\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() {})

	if !res.wroteAny {
		t.Fatal("wroteAny must be true — output already started")
	}
	if res.shed != "" {
		t.Error("must not report a pre-output shed once output has started")
	}
	// The demotion succeeds, so the CLI recovers and nothing else would ever
	// record that upstream refused to serve. Without this the turn reaches the
	// operator only as one that finished with no usage.
	if !res.demoted.shed || !res.demoted.capacity {
		t.Errorf("the post-output capacity shed must be recorded; got %+v", res.demoted)
	}
	out := w.Body.String()
	if strings.Contains(out, "server_is_overloaded") {
		t.Errorf("the session-ending code must be demoted, got %q", out)
	}
	if !strings.Contains(out, "server_error") {
		t.Errorf("expected the demoted code in the output, got %q", out)
	}
	if !strings.Contains(out, "we are full") {
		t.Errorf("the upstream message must survive verbatim, got %q", out)
	}
	if !strings.Contains(out, "partial") {
		t.Errorf("earlier output must still be present, got %q", out)
	}
}

// Quota and rate codes are account-scoped rather than capacity-scoped: the CLI
// already handles them non-terminally and parses its retry delay off the
// original code, so they must reach the client untouched. They are still a shed
// — upstream refused the turn — and must be recorded as one, or half the
// post-output sheds stay invisible.
func TestStreamSSECodexBackendRecordsQuotaShedAfterOutputWithoutDemoting(t *testing.T) {
	for _, code := range []string{"insufficient_quota", "rate_limit_exceeded", "usage_not_included"} {
		t.Run(code, func(t *testing.T) {
			c, w := newCodexStreamCtx()
			body := "event: response.output_text.delta\n" +
				`data: {"type":"response.output_text.delta","delta":"partial"}` + "\n\n" +
				"event: error\n" +
				`data: {"type":"error","error":{"code":"` + code + `","message":"out of credit"}}` + "\n\n"
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

			var counts usage.Counts
			res := streamSSECodexBackend(c, resp, &counts, func() {})

			if !res.demoted.shed {
				t.Error("a quota/rate frame after output is still a shed and must be recorded")
			}
			if res.demoted.capacity {
				t.Error("quota/rate is not a capacity shed and must not be reported as one")
			}
			out := w.Body.String()
			if !strings.Contains(out, code) {
				t.Errorf("the original code must reach the client untouched, got %q", out)
			}
			if strings.Contains(out, "server_error") {
				t.Errorf("quota/rate must never be demoted, got %q", out)
			}
		})
	}
}

// A fatal error (the request's own fault) must reach the client untouched —
// retrying it on another credential would fail identically.
func TestStreamSSECodexBackendForwardsFatalErrorVerbatim(t *testing.T) {
	c, w := newCodexStreamCtx()
	frame := `{"type":"error","error":{"type":"invalid_request_error","code":"content_policy_violation","message":"blocked"}}`
	body := "event: error\ndata: " + frame + "\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() {})

	if res.shed != "" {
		t.Error("a fatal error must not be shed")
	}
	if res.demoted.shed {
		t.Error("a fatal error is the request's own fault, not an upstream shed")
	}
	if !res.wroteAny {
		t.Error("a fatal error must reach the client")
	}
	if out := w.Body.String(); !strings.Contains(out, frame) {
		t.Errorf("fatal frame must be forwarded verbatim, got %q", out)
	}
}

// The real shape of a shed turn: upstream opens with response.created (and
// usually response.in_progress), then refuses. Before the preamble buffer those
// openers committed the response, so the withhold above could never fire — the
// sibling fork measured it triggering zero times in production while 52 sheds in
// the same window were forced onto the demote path. Buffering the openers keeps
// the response uncommitted long enough to fail over, and the client sees nothing
// at all.
func TestStreamSSECodexBackendShedsAfterPreambleOnly(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "slow_down", "rate_limit_exceeded"} {
		t.Run(code, func(t *testing.T) {
			c, w := newCodexStreamCtx()
			body := "event: response.created\n" +
				`data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n" +
				"event: response.in_progress\n" +
				`data: {"type":"response.in_progress","response":{"id":"resp_1"}}` + "\n\n" +
				"event: error\n" +
				`data: {"type":"error","error":{"code":"` + code + `","message":"we are full"}}` + "\n\n" +
				"event: response.failed\n" +
				`data: {"type":"response.failed","response":{"error":{"code":"` + code + `"}}}` + "\n\n"
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

			committed := false
			var counts usage.Counts
			res := streamSSECodexBackend(c, resp, &counts, func() { committed = true })

			if committed {
				t.Error("commit() must not run — a turn that produced only openers is still fully retryable")
			}
			if res.wroteAny {
				t.Error("wroteAny must be false so the caller's pre-output failover fires")
			}
			if res.sawTerminal {
				t.Error("sawTerminal must be false — the withheld response.failed must not look like a clean end")
			}
			if res.shed == "" {
				t.Error("shed must carry the withheld frame so the caller retries instead of giving up")
			}
			if got := w.Body.String(); got != "" {
				t.Errorf("nothing may reach the client, got %q", got)
			}
		})
	}
}

// The buffer must not swallow or reorder anything on a healthy turn: the openers
// are released, in upstream's original order, as soon as real content arrives.
func TestStreamSSECodexBackendReleasesPreambleOnContent(t *testing.T) {
	c, w := newCodexStreamCtx()
	body := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","delta":"hello"}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}` + "\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() {})

	if !res.sawTerminal {
		t.Error("a completed stream must still report sawTerminal")
	}
	out := w.Body.String()
	for _, want := range []string{"response.created", "response.output_text.delta", "response.completed"} {
		if !strings.Contains(out, want) {
			t.Errorf("%s must reach the client, got %q", want, out)
		}
	}
	if i, j := strings.Index(out, "response.created"), strings.Index(out, "response.output_text.delta"); i > j {
		t.Error("the buffered opener must be released before the content that flushed it")
	}
	if counts.InputTokens != 10 || counts.OutputTokens != 5 {
		t.Errorf("usage must survive the buffering: in=%d out=%d", counts.InputTokens, counts.OutputTokens)
	}
}

// A stream that ends after nothing but openers must still release them rather
// than handing the client an empty body.
func TestStreamSSECodexBackendReleasesPreambleOnEOF(t *testing.T) {
	c, w := newCodexStreamCtx()
	body := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n"
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(body))}

	var counts usage.Counts
	res := streamSSECodexBackend(c, resp, &counts, func() {})

	if res.shed != "" {
		t.Errorf("a plain truncation is not a shed; got %q", res.shed)
	}
	if !strings.Contains(w.Body.String(), "response.created") {
		t.Errorf("the buffered opener must not be swallowed on EOF, got %q", w.Body.String())
	}
}

// A non-streaming turn is shed the same way a streaming one is: an error frame
// inside an otherwise-200 stream. This path did not look for it, so aggregation
// ran on to EOF and reported a truncation — which the caller does retry, but
// under a name that blames the wire rather than upstream capacity.
func TestAggregateCodexResponseStreamReportsShed(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "slow_down", "rate_limit_exceeded"} {
		t.Run(code, func(t *testing.T) {
			body := "event: response.created\n" +
				`data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n" +
				"event: error\n" +
				`data: {"type":"error","error":{"code":"` + code + `","message":"nope"}}` + "\n\n"
			var counts usage.Counts
			out, shed, err := aggregateCodexResponseStream(strings.NewReader(body), &counts)

			if shed == "" {
				t.Error("a shed must be reported so the caller fails over under the right name")
			}
			if err != nil {
				t.Errorf("a shed is not an aggregation error; got %v", err)
			}
			if out != nil {
				t.Errorf("no payload may be produced from a shed turn; got %q", out)
			}
		})
	}
}

// The dangerous variant the shed check also closes: a relay that sends the error
// frame and then response.completed would otherwise hand back a "successful"
// payload carrying an error, with no retry and no trace.
func TestAggregateCodexResponseStreamShedBeatsCompleted(t *testing.T) {
	body := "event: error\n" +
		`data: {"type":"error","error":{"code":"server_is_overloaded","message":"full"}}` + "\n\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"r","output":[]}}` + "\n\n"
	var counts usage.Counts
	out, shed, err := aggregateCodexResponseStream(strings.NewReader(body), &counts)
	if shed == "" {
		t.Error("the shed must win over the response.completed that follows it")
	}
	if out != nil || err != nil {
		t.Errorf("no payload and no error may be produced; got out=%q err=%v", out, err)
	}
}

// A genuinely truncated stream is still an error, not a shed — the two must stay
// distinguishable so the log says which one happened.
func TestAggregateCodexResponseStreamTruncationIsNotShed(t *testing.T) {
	body := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_1"}}` + "\n\n"
	var counts usage.Counts
	_, shed, err := aggregateCodexResponseStream(strings.NewReader(body), &counts)
	if shed != "" {
		t.Errorf("a truncation is not a shed; got %q", shed)
	}
	if err == nil {
		t.Error("a stream ending without response.completed must report an error")
	}
}

// The happy path must be untouched by the shed check.
func TestAggregateCodexResponseStreamStillAggregates(t *testing.T) {
	body := "event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"r","output":[],"usage":{"input_tokens":7,"output_tokens":3}}}` + "\n\n"
	var counts usage.Counts
	out, shed, err := aggregateCodexResponseStream(strings.NewReader(body), &counts)
	if err != nil || shed != "" {
		t.Fatalf("clean stream must aggregate: shed=%q err=%v", shed, err)
	}
	if len(out) == 0 {
		t.Error("a completed stream must produce a payload")
	}
	if counts.InputTokens != 7 || counts.OutputTokens != 3 {
		t.Errorf("usage must be collected: in=%d out=%d", counts.InputTokens, counts.OutputTokens)
	}
}
