package auth

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// CodexIDTokenClaims captures the claims we actually consume from an OpenAI
// ID token. The full payload has many more fields — we only parse what we
// route on so we don't couple to the provider's JWT schema beyond need.
type CodexIDTokenClaims struct {
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Sub           string `json:"sub"`
	// Custom claim namespace OpenAI uses for Codex-specific fields.
	CodexAuthInfo struct {
		ChatgptAccountID string `json:"chatgpt_account_id"`
		ChatgptPlanType  string `json:"chatgpt_plan_type"`
		ChatgptUserID    string `json:"chatgpt_user_id"`
	} `json:"https://api.openai.com/auth"`
}

// AccountID returns the ChatGPT account ID from the token, or empty string.
func (c *CodexIDTokenClaims) AccountID() string { return c.CodexAuthInfo.ChatgptAccountID }

// PlanType returns the subscription plan (free/plus/pro/team/business/...)
// in its raw case; normalization to our canonical ids happens at routing
// time in auth.NormalizeCodexPlan.
func (c *CodexIDTokenClaims) PlanType() string { return c.CodexAuthInfo.ChatgptPlanType }

// ParseCodexIDToken decodes a JWT **without** verifying its signature — the
// caller has just received the token from auth.openai.com over TLS, so the
// signature has already been checked by the issuer. This mirrors the Codex
// CLI's own behavior.
func ParseCodexIDToken(token string) (*CodexIDTokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT payload: %w", err)
	}
	var claims CodexIDTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal JWT claims: %w", err)
	}
	return &claims, nil
}

func base64URLDecode(s string) ([]byte, error) {
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// Canonical Codex plan identifiers — match CLIProxyAPI's tier buckets so the
// same model sets apply. "go" is OpenAI's internal label for Team.
const (
	CodexPlanFree = "free"
	CodexPlanPlus = "plus"
	CodexPlanPro  = "pro"
	CodexPlanTeam = "team"
)

// NormalizeCodexPlan collapses OpenAI's plan labels into the four tiers our
// pricing/model-visibility logic cares about. Unknown values default to
// "pro" — matches CLIProxyAPI's default for safety (don't accidentally
// restrict a Team/Enterprise account to the Free model list).
func NormalizeCodexPlan(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "free":
		return CodexPlanFree
	case "plus":
		return CodexPlanPlus
	case "pro":
		return CodexPlanPro
	case "team", "business", "go":
		return CodexPlanTeam
	case "":
		return CodexPlanPro
	default:
		return CodexPlanPro
	}
}
