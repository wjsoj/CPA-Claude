package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/wjsoj/cc-core/auth"
)

// upstreamFault classifies why an upstream exchange did not produce a usable
// response. The distinction drives two separate decisions that used to be
// conflated in a single `default: MarkFailure()` branch:
//
//   - whether the failure counts against the credential's health (only faults
//     the credential or the upstream is responsible for should), and
//   - whether retrying the same request on a *different* credential can
//     plausibly help (it cannot when the client's own request is malformed).
//
// This is the standard API-gateway split between client-side and server-side
// errors: counting a client's 400 toward a circuit breaker lets one broken
// caller trip the breaker for every other caller sharing the channel.
type upstreamFault int

const (
	// faultNone — the exchange succeeded and satisfied the response contract.
	faultNone upstreamFault = iota

	// faultClient — the caller's own request is at fault (malformed body,
	// unsupported route, payload too large). Every credential would return
	// the same thing, so this is forwarded verbatim, is never retried, and
	// never touches credential health.
	faultClient

	// faultUpstream — the upstream service, the relay in front of it, or the
	// network between us failed: transport errors, throttling, gateway
	// errors, and responses that violate the API contract (see
	// validateAnthropicResponse). Counts toward health and retries on another
	// credential, but is not a verdict on this credential's validity.
	faultUpstream

	// faultCredential — this specific credential is not usable: revoked,
	// forbidden, or out of funds. Counts toward health with the heaviest
	// weight and retries on another credential.
	faultCredential
)

func (f upstreamFault) retryable() bool {
	return f == faultUpstream || f == faultCredential
}

// classifyUpstreamStatus maps an upstream HTTP status onto an upstreamFault.
//
// The 4xx range is deliberately split rather than treated as one bucket. A
// 400 (bad request body), 404 (route not implemented by this relay), 413
// (payload too large) or 422 (unprocessable) are properties of the *request*
// — retrying them on another credential burns an upstream round-trip to
// arrive at the identical error, and counting them toward credential health
// lets a single misbehaving client degrade a channel that is working
// perfectly for everyone else.
//
// 408 and 429 are the exceptions inside 4xx: both describe the upstream's
// current condition (timed out, throttling) rather than a defect in the
// request, so they classify as faultUpstream and do retry.
//
// Anything unrecognised in 4xx is treated as faultClient — the conservative
// direction, since it neither burns retries nor pollutes health state.
func classifyUpstreamStatus(status int) upstreamFault {
	switch {
	case status < 400:
		return faultNone
	case status == http.StatusUnauthorized,
		status == http.StatusPaymentRequired,
		status == http.StatusForbidden:
		return faultCredential
	case status == http.StatusRequestTimeout,
		status == http.StatusTooManyRequests:
		return faultUpstream
	case status >= 500:
		return faultUpstream
	default:
		return faultClient
	}
}

// reportAnthropicAPIKeyFault records an upstream-side fault on an Anthropic
// API-key credential, mirroring reportCodexAPIKeyFault so the two providers
// agree on what an upstream failure means.
//
// The 429 split is the point of this function. A 429 used to take the generic
// `MarkFailure("upstream 429")` branch, which threw away the one thing the
// upstream actually told us: how long to wait. Every other path in this
// codebase — the Anthropic OAuth path and the Codex API-key path — routes 429
// through pool.ReportUpstreamError, which applies the throttling machinery
// (escalating cooldown, `Retry-After` honoured when present) instead of a
// counter tick. This path now does the same.
//
// The Retry-After value is passed through EXACTLY as the upstream sent it. The
// backoff policy lives in cc-core, not here: inferring our own interval on top
// of an upstream that already answered the question is how two layers end up
// disagreeing about when a channel may be probed again.
//
//	429            → pool.ReportUpstreamError (Retry-After aware)
//	401/402/403    → MarkHardFailure: definitive rejection, pauses immediately
//	5xx/transport/ → MarkFailure: pauses after several in a row
//	contract/200+error
func (s *Server) reportAnthropicAPIKeyFault(a *auth.Auth, status int, resetAt time.Time, detail string) {
	if status == http.StatusTooManyRequests {
		s.pool.ReportUpstreamError(a, status, resetAt)
		return
	}
	if classifyUpstreamStatus(status) == faultCredential {
		a.MarkHardFailure(fmt.Sprintf("upstream %d", status))
		return
	}
	if detail == "" {
		detail = fmt.Sprintf("upstream %d", status)
	}
	a.MarkFailure(detail)
}

// responseOutcome is the verdict on a <400 exchange that can only be reached
// AFTER the body has been relayed — the half of the health signal a status
// line cannot carry.
//
// # Why a 200 can be a failure
//
// The status line is written before the model has produced a single token, so
// on this proxy it says almost nothing about whether the exchange worked.
// Three shapes routinely arrive under a 200:
//
//   - a JSON body that is actually an error envelope (`{"type":"error",…}` /
//     `{"error":{…}}`) — the standard way a relay reports its own backend
//     failing after it has already committed the status line;
//   - an SSE stream whose only event is `event: error` — same thing in the
//     streaming shape, and the shape a relay must use because SSE cannot
//     retract a 200;
//   - a stream that stops before its terminal `message_stop`.
//
// Marking those healthy is not a cosmetic accounting error. MarkSuccess zeroes
// ConsecutiveFailures/Consecutive429s/Consecutive401s and clears the API-key
// breaker's QuarantineUntil/QuarantineStrikes outright. A relay alternating
// between a genuine 500 and a 200-wrapped error therefore never accumulated a
// single strike: every second request wiped the counter, so the breaker never
// opened and rotation never happened. The channel stayed "green" while serving
// nothing.
//
// So: an error payload under a 200 is a FAILURE, deliberately, not a neutral
// event. This will make a batch of traffic that used to be recorded as success
// start accumulating strikes, and some channels will go red shortly after
// deploy. That is the truth surfacing, not a regression — those channels were
// already broken; only the bookkeeping was wrong.
//
// A truncation is the one genuinely ambiguous case and stays NEUTRAL (neither
// MarkSuccess nor MarkFailure): the cut usually comes from several hops
// upstream, so it convicts the wrong party — but letting it reset the failure
// counter would mask a channel that is genuinely deteriorating. This matches
// the Codex path (codex_proxy.go's truncatedStream).
type responseOutcome struct {
	// errorPayload — the 200 carried an error envelope or an `event: error`
	// SSE frame.
	errorPayload bool
	// truncated — a stream ended without its terminal event.
	truncated bool
	// clientGone — the caller hung up. Never the credential's fault.
	clientGone bool
	// detail is a short human-readable reason for the log/health note.
	detail string
}

// markResponseHealth applies the deferred health verdict for an exchange whose
// status line was <400.
//
// It is deliberately the ONLY place a success is recorded on these paths, and
// it runs after the body has been fully relayed — a MarkSuccess issued before
// any bytes flowed is a statement about the status line, not about the
// exchange (see responseOutcome).
//
// Statuses >=400 are not this function's business: the callers already made a
// credential judgement for them (faultClient leaves health alone, faultUpstream
// and faultCredential were reported before the response was withheld).
func markResponseHealth(a *auth.Auth, status int, o responseOutcome) {
	if a == nil || status >= 400 {
		return
	}
	switch {
	case o.errorPayload:
		// An explicit upstream error is the strongest signal available, and it
		// outranks a client disconnect: the error was already on the wire.
		a.MarkFailure(fmt.Sprintf("upstream %d with error payload: %s", status, o.detail))
	case o.clientGone:
		// The user walked away. Never a credential fault, and never a success
		// either — MarkClientCancel records a timestamp only.
		a.MarkClientCancel(o.detail)
	case o.truncated:
		// Health-neutral by design; see responseOutcome.
	default:
		a.MarkSuccess()
	}
}

// bodyLooksLikeAPIError reports whether a <400 JSON body is really an error
// envelope. Both the Anthropic shape (`{"type":"error","error":{…}}`) and the
// looser relay shape (`{"error":…}` with no type) count — relays commonly emit
// the latter when their own backend fails after the status line is committed.
//
// A genuine Messages response never carries a non-null top-level `error`, so
// this cannot false-positive on real traffic. Anything that is not a JSON
// object (SSE, HTML, empty) is not this function's problem —
// validateAnthropicResponse already rejects those.
func bodyLooksLikeAPIError(body []byte) (bool, string) {
	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false, ""
	}
	var probe struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		// Unparseable JSON under a 200 is its own problem, but it is not an
		// *error envelope* — leave that judgement to the contract check.
		return false, ""
	}
	hasError := len(probe.Error) > 0 && !bytes.Equal(bytes.TrimSpace(probe.Error), []byte("null"))
	if probe.Type == "error" || hasError {
		return true, truncate(trimmed, 200)
	}
	return false, ""
}

// contractViolation describes a 2xx/3xx response whose body does not conform
// to the Anthropic Messages API wire format. Empty Detail means no violation.
type contractViolation struct {
	Detail string
}

// validateAnthropicResponse checks that a non-error response actually carries
// an Anthropic-shaped payload before we commit it to the client and bill it.
//
// Status codes alone are not a health signal for a gateway. A dead or
// misconfigured relay in front of api.anthropic.com typically answers
// `200 OK` with an HTML interstitial ("Access restricted", a login wall, a
// CDN block page). Trusting the status code makes the proxy mark the
// credential healthy, stream an empty response to the client, and bill zero
// tokens — a silent failure that is only visible afterwards by noticing the
// credential logged nothing but zero-token rows.
//
// The check is deliberately shape-based, not Content-Type-based: relays are
// known to stream a perfectly valid SSE body under `text/plain` (the Codex
// path already compensates for exactly this). So a response passes when its
// first bytes look like either an Anthropic JSON object or an SSE field
// line, and fails otherwise. That is narrow enough to admit every legitimate
// upstream — including quirky relays — while still rejecting the HTML pages
// that caused the silent-failure incident.
//
// Peeking is non-consuming, so the same buffered reader is handed to the SSE
// relay or the whole-body read afterwards.
func validateAnthropicResponse(h http.Header, br *bufio.Reader) contractViolation {
	if looksLikeSSE(br) || looksLikeJSONObject(br) {
		return contractViolation{}
	}
	peek, _ := br.Peek(256)
	if len(peek) == 0 {
		// A 200 with a completely empty body carries no message and no usage.
		// Treat it as a failed exchange rather than a free, zero-token success.
		return contractViolation{Detail: "empty body"}
	}
	ct := h.Get("Content-Type")
	if ct == "" {
		ct = "(none)"
	}
	return contractViolation{
		Detail: "content-type=" + ct + " body=" + strings.TrimSpace(string(peek)),
	}
}

// looksLikeJSONObject peeks the head of a buffered reader and reports whether
// the first non-whitespace byte opens a JSON object. Non-consuming.
//
// Only `{` counts: every Anthropic Messages response — success or error — is
// an object. A bare array, string, or number would be as much of a contract
// violation as HTML.
func looksLikeJSONObject(br *bufio.Reader) bool {
	peek, _ := br.Peek(512)
	trimmed := bytes.TrimLeft(peek, " \t\r\n")
	return len(trimmed) > 0 && trimmed[0] == '{'
}
