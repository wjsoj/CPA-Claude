package server

import (
	"bytes"
	"encoding/json"
	"errors"
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
		rewritten, err := rewriteModelFieldPreservingBytes(working, upstreamModel)
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

// rewriteModelFieldPreservingBytes sets the top-level "model" string without
// disturbing any other byte.
//
// rewriteModelField (the map round-trip below in proxy.go) reorders every
// top-level key alphabetically, which turns the captured Claude Code key order
// into one no real client emits. That is tolerable on the API-key relay path it
// serves; it is not on a body we are about to forward on an OAuth credential.
//
// Duplicated from hypitoken for now. cc-core ships this as
// mimicry.RewriteModelFieldPreservingBytes — collapse both copies onto it at
// the next dependency bump.
func rewriteModelFieldPreservingBytes(body []byte, upstream string) ([]byte, error) {
	span, err := requireTopLevelJSONFieldSpan(body, "model")
	if err != nil {
		return nil, err
	}
	var current string
	if err = json.Unmarshal(body[span.start:span.end], &current); err != nil {
		return nil, errors.New("top-level model is not a JSON string")
	}
	mb, err := json.Marshal(upstream)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 0, len(body)-span.end+span.start+len(mb))
	out = append(out, body[:span.start]...)
	out = append(out, mb...)
	out = append(out, body[span.end:]...)
	return out, nil
}

type jsonFieldSpan struct {
	start int
	end   int
}

func requireTopLevelJSONFieldSpan(body []byte, name string) (jsonFieldSpan, error) {
	if !json.Valid(body) {
		return jsonFieldSpan{}, errors.New("request body is not valid JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	opening, err := dec.Token()
	if err != nil || opening != json.Delim('{') {
		return jsonFieldSpan{}, errors.New("request body is not a JSON object")
	}

	var found jsonFieldSpan
	count := 0
	for dec.More() {
		keyToken, tokenErr := dec.Token()
		if tokenErr != nil {
			return jsonFieldSpan{}, tokenErr
		}
		key, ok := keyToken.(string)
		if !ok {
			return jsonFieldSpan{}, errors.New("JSON object key is not a string")
		}

		before := int(dec.InputOffset())
		var raw json.RawMessage
		if decodeErr := dec.Decode(&raw); decodeErr != nil {
			return jsonFieldSpan{}, decodeErr
		}
		after := int(dec.InputOffset())
		if before < 0 || after < before || after > len(body) || len(raw) == 0 {
			return jsonFieldSpan{}, errors.New("invalid JSON decoder offsets")
		}
		relativeStart := bytes.LastIndex(body[before:after], raw)
		if relativeStart < 0 {
			return jsonFieldSpan{}, errors.New("could not locate raw JSON value")
		}
		if key == name {
			count++
			start := before + relativeStart
			found = jsonFieldSpan{start: start, end: start + len(raw)}
		}
	}
	if _, err = dec.Token(); err != nil {
		return jsonFieldSpan{}, err
	}
	if count == 0 {
		return jsonFieldSpan{}, fmt.Errorf("JSON object has no %q field", name)
	}
	if count != 1 {
		return jsonFieldSpan{}, fmt.Errorf("JSON object has duplicate %q fields", name)
	}
	return found, nil
}
