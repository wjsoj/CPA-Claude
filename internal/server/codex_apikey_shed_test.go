package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/usage"
)

// Upstream sheds load as an in-band error frame inside an otherwise-200 SSE
// stream. Forwarded verbatim, `server_is_overloaded` reaches the Codex CLI as
// ApiError::ServerOverloaded — TERMINAL for the session, surfacing to the user
// as "Our servers are currently overloaded". The same failure under nearly any
// other code lands in the CLI's Retryable arm and is merely backed off.
//
// The OAuth relay has demoted these two codes since cc-core/codexerr landed;
// this relay did not, so every capacity shed on an API-key credential killed the
// user's session outright.
func runAPIKeySSE(t *testing.T, sse string) (*httptest.ResponseRecorder, usage.Counts, sseRelayOutcome) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	var counts usage.Counts
	out := streamSSEOpenAI(c, bufio.NewReader(strings.NewReader(sse)), &counts, "")
	return w, counts, out
}

func TestAPIKeyStreamDemotesServerOverloaded(t *testing.T) {
	for _, code := range []string{"server_is_overloaded", "slow_down"} {
		t.Run(code, func(t *testing.T) {
			sse := `data: {"type":"error","error":{"code":"` + code + `","message":"Our servers are currently overloaded"}}` + "\n\n"
			w, _, out := runAPIKeySSE(t, sse)

			body := w.Body.String()
			if strings.Contains(body, code) {
				t.Errorf("the session-ending capacity code must not reach the client; got %q", body)
			}
			if !strings.Contains(body, "server_error") {
				t.Errorf("the code must be demoted to server_error so the CLI retries; got %q", body)
			}
			// The human-readable message must survive — the user still needs to
			// know why the turn failed.
			if !strings.Contains(body, "Our servers are currently overloaded") {
				t.Errorf("the upstream message must be preserved verbatim; got %q", body)
			}
			if !out.shed || !out.capacity {
				t.Errorf("the shed must be reported as capacity; got shed=%v capacity=%v", out.shed, out.capacity)
			}
		})
	}
}

// Quota and rate codes are account-scoped, not moment-scoped. The CLI handles
// them non-terminally already and parses its retry delay off the original code,
// so demoting them would destroy information for no gain.
func TestAPIKeyStreamDoesNotDemoteQuotaCodes(t *testing.T) {
	sse := `data: {"type":"error","error":{"code":"insufficient_quota","message":"out of credit"}}` + "\n\n"
	w, _, out := runAPIKeySSE(t, sse)

	if !strings.Contains(w.Body.String(), "insufficient_quota") {
		t.Errorf("a quota code must reach the client untouched; got %q", w.Body.String())
	}
	if !out.shed {
		t.Error("a quota frame is still a shed turn")
	}
	if out.capacity {
		t.Error("a quota code is not a capacity shed and must not be demoted")
	}
}

// A fatal error is the request's own fault: retrying elsewhere would fail
// identically, so it passes through untouched and is not counted as a shed.
func TestAPIKeyStreamForwardsFatalErrorVerbatim(t *testing.T) {
	frame := `{"type":"error","error":{"type":"invalid_request_error","code":"content_policy_violation","message":"blocked"}}`
	w, _, out := runAPIKeySSE(t, "data: "+frame+"\n\n")

	if out.shed {
		t.Error("a fatal error is not a shed")
	}
	if !strings.Contains(w.Body.String(), "content_policy_violation") {
		t.Errorf("a fatal frame must be forwarded verbatim; got %q", w.Body.String())
	}
}

// Terminal detection has to cover both wire formats this relay serves:
// /v1/responses ends with a response.completed-family event, while
// /v1/chat/completions ends with the [DONE] sentinel. Without one the client
// reports "stream disconnected before completion".
func TestAPIKeyStreamTracksTerminalEvent(t *testing.T) {
	cases := []struct {
		name string
		sse  string
		want bool
	}{
		{
			name: "responses terminal",
			sse:  `data: {"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":5}}}` + "\n\n",
			want: true,
		},
		{
			name: "chat DONE sentinel",
			sse:  `data: {"id":"1","object":"chat.completion.chunk"}` + "\n\n" + "data: [DONE]\n\n",
			want: true,
		},
		{
			name: "truncated upstream",
			sse:  `data: {"type":"response.output_text.delta","delta":"hel"}` + "\n\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, out := runAPIKeySSE(t, tc.sse)
			if out.sawTerminal != tc.want {
				t.Errorf("sawTerminal = %v, want %v", out.sawTerminal, tc.want)
			}
		})
	}
}

// Billing must be read from the ORIGINAL payload: demotion rewrites the code,
// and a shed turn still has to account for whatever usage upstream reported
// before it gave up.
func TestAPIKeyStreamBillsFromOriginalPayload(t *testing.T) {
	sse := `data: {"type":"response.completed","response":{"usage":{"input_tokens":42,"output_tokens":7}}}` + "\n\n"
	_, counts, _ := runAPIKeySSE(t, sse)
	if counts.InputTokens != 42 || counts.OutputTokens != 7 {
		t.Errorf("usage = in:%d out:%d, want in:42 out:7", counts.InputTokens, counts.OutputTokens)
	}
}
