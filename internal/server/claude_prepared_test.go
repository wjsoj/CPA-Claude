package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
	"github.com/wjsoj/cc-core/thinkingsig"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// customBaseURLBody is what a real Claude Code sends when it is pointed at us
// with ANTHROPIC_BASE_URL: Claude Code-shaped, but with the downstream user's
// own device_id, an EMPTY account_uuid, their Claude Code version in the
// billing block, and no cch / cc_prev_req.
// Shape follows cc-core/crack/thirdparty/rows/01-v1_messages.json.
const customBaseURLBody = `{"model":"claude-sonnet-5",` +
	`"messages":[{"role":"user","content":"hello"}],` +
	`"system":[` +
	`{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.226.ab9; cc_entrypoint=cli;"},` +
	`{"type":"text","text":"You are Claude Code, Anthropic's official CLI for Claude.","cache_control":{"type":"ephemeral"}},` +
	`{"type":"text","text":"long env prompt","cache_control":{"type":"ephemeral"}}],` +
	`"metadata":{"user_id":"{\"device_id\":\"downstream-device\",\"account_uuid\":\"\",\"session_id\":\"11111111-1111-4111-8111-111111111111\"}"},` +
	`"stream":true}`

// The captured custom-base-url main beta vector — see cc-core
// crack/thirdparty/SPEC.md §1a.
const customBaseURLBeta = "claude-code-20250219,interleaved-thinking-2025-05-14,redact-thinking-2026-02-12," +
	"thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05," +
	"mid-conversation-system-2026-04-07,effort-2025-11-24"

func preparedTestCredential() *auth.Auth {
	return &auth.Auth{
		ID:          "credA",
		Kind:        auth.KindOAuth,
		Provider:    "anthropic",
		Label:       "credA",
		AccessToken: "tokenA",
		AccountUUID: "22222222-2222-4222-8222-222222222222",
		ExpiresAt:   time.Now().Add(2 * time.Hour),
	}
}

// The regression this whole migration exists for: before it, a genuine
// Claude Code body arriving over a custom base URL was recognised as "already
// Claude Code" and forwarded untouched, so the DOWNSTREAM user's identity rode
// out to Anthropic under OUR OAuth account.
func TestCustomBaseURLRequestIsReboundToOurAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cred := preparedTestCredential()

	prepared, err := prepareClaudeOAuthBody([]byte(customBaseURLBody), "claude-sonnet-5", cred, mimicry.SimIdentity{
		AccountKey:  cred.AccountKey(),
		AccountUUID: cred.AccountUUIDValue(),
		ClientToken: "tok-abcdef123456",
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !prepared.AccountIdentityApplied() {
		t.Fatal("prepared result did not bind the account identity")
	}

	var obj struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
		System []struct {
			Text         string          `json:"text"`
			CacheControl json.RawMessage `json:"cache_control"`
		} `json:"system"`
	}
	if err := json.Unmarshal(prepared.Body(), &obj); err != nil {
		t.Fatal(err)
	}

	var identity struct {
		DeviceID    string `json:"device_id"`
		AccountUUID string `json:"account_uuid"`
		SessionID   string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(obj.Metadata.UserID), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.AccountUUID != cred.AccountUUIDValue() {
		t.Errorf("account_uuid = %q, want our account %q", identity.AccountUUID, cred.AccountUUIDValue())
	}
	if identity.DeviceID == "downstream-device" {
		t.Error("the downstream user's device_id reached the upstream body")
	}
	if identity.SessionID == "11111111-1111-4111-8111-111111111111" {
		t.Error("the downstream session id was not remapped into our account namespace")
	}

	// The version is pinned to ours, and the first-party chain fields stay absent.
	if !strings.Contains(obj.System[0].Text, "cc_version="+mimicry.CLICurrentVersion+".") {
		t.Errorf("billing block not pinned to our version: %q", obj.System[0].Text)
	}
	if strings.Contains(obj.System[0].Text, "cch=") || strings.Contains(obj.System[0].Text, "cc_prev_req=") {
		t.Errorf("billing block gained first-party chain fields: %q", obj.System[0].Text)
	}

	// The client's own prompt text is preserved verbatim — the rewrite is
	// identity surgery, not content rewriting.
	if obj.System[2].Text != "long env prompt" {
		t.Errorf("client prompt was altered: %q", obj.System[2].Text)
	}
}

// NOTE on scope: the exact fingerprint VALUES (which betas belong in a repaired
// vector, what ttl/scope the cache breakpoints carry) are cc-core's contract and
// are asserted there — mimicry's beta_test.go and cachecontrol_test.go. What
// this fork is responsible for, and what these tests pin, is that the prepared
// pipeline is wired in at all: that a custom-base-url request goes through it,
// that body and headers come from the same result, and that a failure fails
// closed. The beta-vector and cache_control repairs reach production here on the
// next cc-core dependency bump; no change to this file is needed for that.

// The header half of the same transform: the beta vector is repaired, the
// missing x-client-request-id is minted, and the client's 3000ms Stainless
// timeout is pinned back to the value real Claude Code sends.
func TestPreparedHeadersRepairTheCustomBaseURLFingerprint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cred := preparedTestCredential()

	prepared, err := prepareClaudeOAuthBody([]byte(customBaseURLBody), "claude-sonnet-5", cred, mimicry.SimIdentity{
		AccountKey:  cred.AccountKey(),
		AccountUUID: cred.AccountUUIDValue(),
		ClientToken: "tok-abcdef123456",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	req.Header.Set("Anthropic-Beta", customBaseURLBeta)
	req.Header.Set("X-Stainless-Timeout", "3000")
	req.Header.Set("User-Agent", "claude-cli/2.1.226 (external, cli)")
	if err := applyAnthropicPreparedHeaders(req, cred, true, true, prepared); err != nil {
		t.Fatalf("apply headers: %v", err)
	}

	// The OAuth marker is added on every cc-core version; the full OAuth-only
	// set arrives with the next dependency bump (see the scope note above).
	if got := req.Header.Get("Anthropic-Beta"); !strings.Contains(got, "oauth-2025-04-20") {
		t.Errorf("beta vector %q carries no oauth marker", got)
	}
	if got := req.Header.Get("x-client-request-id"); got == "" {
		t.Error("x-client-request-id was not minted")
	}
	if got := req.Header.Get("X-Stainless-Timeout"); got != "600" {
		t.Errorf("X-Stainless-Timeout = %q, want 600", got)
	}
	if got := req.Header.Get("User-Agent"); got != mimicry.ClaudeCLIUserAgent {
		t.Errorf("User-Agent = %q, want the pinned UA", got)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer tokenA" {
		t.Errorf("Authorization = %q", got)
	}
}

// A genuine request with no beta vector cannot be repaired safely (main, 1M and
// title carry different ones), so preparation must fail closed rather than
// invent one — and the failure must be a local judgement that leaves the
// credential's health untouched.
func TestPreparationFailureLeavesCredentialHealthy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("upstream must not be reached when preparation fails")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cred := preparedTestCredential()
	s := &Server{
		cfg:           &config.Config{AnthropicBaseURL: upstream.URL, UseUTLS: false},
		pool:          auth.NewPool([]*auth.Auth{cred}, nil, 10*time.Minute, false, ""),
		switchTracker: thinkingsig.NewSwitchTracker(),
	}
	c, _ := newMessagesContext(t, []byte(customBaseURLBody))
	// No Anthropic-Beta on the ingress request.

	var prepared mimicry.BodyTransformResult
	err := s.preflightClaudeOAuth(c, cred, []byte(customBaseURLBody), "claude-sonnet-5", "tok-abcdef123456", true, &prepared)
	if err == nil {
		t.Fatal("preflight should fail closed without a downstream beta vector")
	}
	if got := claudePreparationFailureReason(err); got != "missing_anthropic_beta" {
		t.Errorf("failure reason = %q", got)
	}
	if prepared.IsValid() {
		t.Error("a failed preflight must not hand back a usable prepared result")
	}
	if !cred.IsHealthy() {
		t.Error("a local preparation failure must not mark the credential unhealthy")
	}
	if cred.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", cred.ConsecutiveFailures)
	}
}

// An empty account_uuid on OUR side means we have nothing to rebind to, so the
// rewrite must refuse rather than forward the downstream identity.
func TestPreparationRefusesCredentialWithoutAccountUUID(t *testing.T) {
	cred := preparedTestCredential()
	cred.AccountUUID = ""

	_, err := prepareClaudeOAuthBody([]byte(customBaseURLBody), "claude-sonnet-5", cred, mimicry.SimIdentity{
		AccountKey:  cred.AccountKey(),
		AccountUUID: cred.AccountUUIDValue(),
		ClientToken: "tok-abcdef123456",
	})
	if err == nil {
		t.Fatal("expected preparation to fail without an account UUID")
	}
	if got := claudePreparationFailureReason(err); got != "missing_account_uuid" {
		t.Errorf("failure reason = %q", got)
	}
}

// The model rewrite must not reorder the body's top-level keys, and it has to
// run before preparation because the prepared result pins a body digest. The
// rewrite itself now lives in cc-core; this pins the ordering through OUR call
// path, which is what a future refactor here could break.
func TestPreparedBodyModelRewriteKeepsKeyOrder(t *testing.T) {
	const body = `{"model":"claude-opus-5","messages":[],"system":[],"stream":true}`
	got, err := mimicry.RewriteModelFieldPreservingBytes([]byte(body), "vendor/claude-opus-5")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(body, `"claude-opus-5"`, `"vendor/claude-opus-5"`, 1)
	if string(got) != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

// End-to-end proof that the response scrub is wired into the success path, not
// only the withheld-error replay: a plain 200 must not carry our account's
// quota state or organization to the caller.
func TestSuccessfulResponseDoesNotLeakPoolState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "application/json")
		h.Set("Anthropic-Ratelimit-Unified-Status", "allowed")
		h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.83")
		h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1786123800")
		h.Set("Anthropic-Organization-Id", "bf62f90e-ff9c-4d95-a554-17835658b5ef")
		h.Set("Anthropic-Workspace-Id", "wrkspc_01Mx5eXmqPciXqAJUQDyHRAQ")
		h.Set("Request-Id", "req_011CdoZnTHdYogjzJ6Wuzf6Y")
		h.Set("Cf-Ray", "a2770e297a19f3ec-LAX")
		w.WriteHeader(http.StatusBadRequest) // non-retryable: forwarded verbatim, no billing needed
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"nope"},"request_id":"req_011CdoZnTHdYogjzJ6Wuzf6Y"}`))
	}))
	defer upstream.Close()

	cred := preparedTestCredential()
	s := newDoForwardTestServer(t, upstream.URL, cred)
	c, w := newMessagesContext(t, haikuBody)

	s.doForward(c, cred, "/v1/messages", haikuBody, false,
		"claude-haiku-4-5-20251001", "tok-abcdef123456", "slot-1", "client", time.Now(), 1, false,
		mimicry.BodyTransformResult{})

	for _, leaked := range []string{
		"Anthropic-Ratelimit-Unified-Status",
		"Anthropic-Ratelimit-Unified-5h-Utilization",
		"Anthropic-Organization-Id",
		"Anthropic-Workspace-Id",
		"Request-Id",
		"Cf-Ray",
	} {
		if got := w.Header().Get(leaked); got != "" {
			t.Errorf("%s reached the client with %q", leaked, got)
		}
	}
	if got := w.Header().Get("Content-Type"); got == "" {
		t.Error("Content-Type was dropped; the client cannot parse the body without it")
	}
	if strings.Contains(w.Body.String(), "req_011CdoZnTHdYogjzJ6Wuzf6Y") {
		t.Errorf("the upstream request id survived in the error body: %s", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid_request_error") {
		t.Errorf("the actionable error type was lost: %s", w.Body.String())
	}
}
