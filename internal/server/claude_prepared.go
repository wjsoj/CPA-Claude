package server

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/mimicry"
)

// The prepared-request pipeline for Anthropic OAuth traffic.
//
// Our users point their Claude Code at us with ANTHROPIC_BASE_URL, so an
// inbound /v1/messages has the custom-base-url shape recorded in
// cc-core/crack/thirdparty/SPEC.md: its metadata.user_id carries the user's own
// device_id and an EMPTY account_uuid, its billing block names the user's
// Claude Code version, and its beta vector is missing the five OAuth-only
// entitlements. We then forward it to api.anthropic.com on one of our OAuth
// credentials, where it has to look like cc-core/crack/cc2224/rows/13.
//
// The legacy mimicry entry point cannot do that job: ApplyClaudeCodeBodyMimicry
// sees a body that is *already* Claude Code-shaped and returns it untouched, so
// the downstream user's identity rode straight through to Anthropic under our
// account. mimicry.PrepareClaudeCodeRequest is the fail-closed replacement —
// it rebinds the identity, pins the version, repairs the beta vector and the
// cache breakpoints, and verifies its own work before the request is allowed
// out.
//
// Preparation failures are LOCAL judgements, never credential faults: they must
// not call MarkFailure and must not blame the selected credential (see cc-core
// CLAUDE.md). forwardWithFailover preflights this and switches to an API key
// with the untouched body instead.

// preflightClaudeOAuth prepares the request and validates the complete header
// policy for this credential WITHOUT sending anything. On success it stores the
// prepared result so doForward can reuse the exact bytes that were checked.
//
// Running the header step too is what catches a missing genuine Anthropic-Beta,
// an empty credential, or any future prepared-header invariant — before the
// sidecar bootstrap burst has announced this account to Anthropic on behalf of
// a request we then cannot send.
func (s *Server) preflightClaudeOAuth(c *gin.Context, a *auth.Auth, body []byte, model, clientToken string, stream bool, out *mimicry.BodyTransformResult) error {
	prepared, err := prepareClaudeOAuthBody(body, model, a, mimicry.SimIdentity{
		AccountKey:  a.AccountKey(),
		AccountUUID: a.AccountUUIDValue(),
		ClientToken: clientToken,
	})
	if err != nil {
		return err
	}

	baseURL := s.cfg.AnthropicBaseURL
	if override := strings.TrimRight(a.Snapshot().BaseURL, "/"); override != "" {
		baseURL = override
	}
	probe := &http.Request{Header: c.Request.Header.Clone()}
	stripIngressHeaders(probe.Header)
	if err := applyAnthropicPreparedHeaders(probe, a, stream,
		strings.HasPrefix(strings.ToLower(baseURL), "https://api.anthropic.com"), prepared); err != nil {
		return err
	}

	*out = prepared
	return nil
}

func applyAnthropicPreparedHeaders(req *http.Request, a *auth.Auth, stream, isAnthropicBase bool, prepared mimicry.BodyTransformResult) error {
	token, kind := a.Credentials()
	return mimicry.ApplyClaudeCodePreparedRequest(req, token, a.AccountKey(), kindToMimicry(kind), stream, isAnthropicBase, prepared)
}

// prepareClaudeOAuthBody classifies the body and prepares it under the policy
// that class calls for. Genuine Claude Code bodies (which is what a real client
// on a custom base URL sends) get the account-bound rewrite; anything else is
// synthesized into Claude Code shape rather than forwarded as-is.
func prepareClaudeOAuthBody(body []byte, model string, a *auth.Auth, id mimicry.SimIdentity) (mimicry.BodyTransformResult, error) {
	requestClass := mimicry.ClassifyClaudeCodeRequest(body)
	policy, err := claudeRequestPolicy(requestClass)
	if err != nil {
		return mimicry.BodyTransformResult{}, err
	}
	return prepareClaudePreparedBody(body, model, a, id, policy)
}

func claudeRequestPolicy(requestClass mimicry.RequestClass) (mimicry.RequestPolicy, error) {
	switch requestClass {
	case mimicry.RequestClassGenuine:
		return mimicry.NewClaudeCodeRequestPolicy(requestClass, mimicry.GenuineRequestRewrite)
	case mimicry.RequestClassGeneric:
		// Note the separate constructor: NewClaudeCodeRequestPolicy would hand
		// back the legacy best-effort generic mode, whose zero value it is.
		return mimicry.NewGenericClaudeCodeSynthesizePolicy(), nil
	default:
		return mimicry.RequestPolicy{}, fmt.Errorf("unsupported Claude request class %s", requestClass)
	}
}

// prepareClaudePreparedBody applies the edits that are allowed to precede the
// atomic transform, then performs it.
//
// Order is load-bearing. The prepared result pins a sha256 of the body, so a
// model rewrite or dateline scrub afterwards would be rejected at Apply time.
func prepareClaudePreparedBody(body []byte, model string, a *auth.Auth, id mimicry.SimIdentity, policy mimicry.RequestPolicy) (mimicry.BodyTransformResult, error) {
	working := body
	if upstreamModel, ok := a.ResolveUpstreamModel(model); ok && upstreamModel != model && upstreamModel != "" {
		rewritten, err := mimicry.RewriteModelFieldPreservingBytes(working, upstreamModel)
		if err != nil {
			return mimicry.BodyTransformResult{}, fmt.Errorf("model rewrite (%s -> %s): %w", model, upstreamModel, err)
		}
		working = rewritten
	}
	// Erase the client "Today's date is …" steganography beacon real CC embeds
	// once it detects a non-official base URL — it rides the client's own body
	// straight to Anthropic.
	if normalized, changed := mimicry.NormalizeDateline(working); changed {
		working = normalized
	}
	return mimicry.PrepareClaudeCodeRequest(working, model, id, policy, kindToMimicry(a.Kind))
}

// claudePreparationFailureReason maps a preparation error to a stable, low
// cardinality label for logs. Kept in sync with hypitoken's copy so the two
// forks' request logs stay comparable.
func claudePreparationFailureReason(err error) string {
	if err == nil {
		return "unknown"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "account uuid"):
		return "missing_account_uuid"
	case strings.Contains(message, "anthropic-beta"):
		return "missing_anthropic_beta"
	case strings.Contains(message, "messages array"):
		return "invalid_messages"
	case strings.Contains(message, "json object"), strings.Contains(message, "request class"):
		return "invalid_json_body"
	case strings.Contains(message, "metadata"):
		return "metadata_rewrite_failed"
	case strings.Contains(message, "billing"):
		return "billing_rewrite_failed"
	case strings.Contains(message, "model rewrite"):
		return "model_rewrite_failed"
	default:
		return "request_preparation_failed"
	}
}
