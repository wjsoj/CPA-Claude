package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// OpenAI OAuth constants. These match the ChatGPT Codex CLI app (see
// CLIProxyAPI/internal/auth/codex/openai_auth.go) so the consent screen
// the user sees is the same one they'd get from the upstream CLI.
const (
	openaiAuthURL     = "https://auth.openai.com/oauth/authorize"
	openaiRedirectURI = "http://localhost:1455/auth/callback"
	openaiScopes      = "openid email profile offline_access"
)

// buildCodexAuthURL constructs the Codex authorize URL with PKCE + the
// vendor-specific flags the Codex CLI sends (prompt=login,
// id_token_add_organizations, codex_cli_simplified_flow).
func buildCodexAuthURL(state, verifier string) string {
	params := url.Values{
		"client_id":                  {openaiClientID},
		"response_type":              {"code"},
		"redirect_uri":               {openaiRedirectURI},
		"scope":                      {openaiScopes},
		"state":                      {state},
		"code_challenge":             {pkceChallenge(verifier)},
		"code_challenge_method":      {"S256"},
		"prompt":                     {"login"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
	}
	return openaiAuthURL + "?" + params.Encode()
}

// finishCodexLogin exchanges the OAuth code for tokens at OpenAI's token
// endpoint. Unlike the Anthropic side this body is form-urlencoded (not
// JSON) and the response carries an id_token whose claims determine the
// credential's plan_type and account_id.
func finishCodexLogin(
	ctx context.Context,
	sess *LoginSession,
	code, authDir string,
	maxConcurrent int,
	useUTLS bool,
	group string,
) (*Auth, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {openaiClientID},
		"code":          {code},
		"redirect_uri":  {openaiRedirectURI},
		"code_verifier": {sess.CodeVerifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := ClientFor(sess.ProxyURL, useUTLS)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("codex token exchange: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("codex token exchange http %d: %s", resp.StatusCode, string(body))
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("codex token exchange parse: %w", err)
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, fmt.Errorf("codex token exchange returned empty tokens")
	}

	// Decode the id_token for plan_type + account_id + email. Unsigned
	// parse — we just received this over TLS from auth.openai.com; the
	// issuer has already verified it.
	var planType, accountID, email string
	if tr.IDToken != "" {
		if claims, perr := ParseCodexIDToken(tr.IDToken); perr == nil && claims != nil {
			planType = claims.PlanType()
			accountID = claims.AccountID()
			email = claims.Email
		}
	}

	expires := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	label := sess.Label
	if label == "" {
		label = email
	}
	filename := buildCodexCredentialFilename(email, planType, accountID, sess.ID)

	if err := os.MkdirAll(authDir, 0700); err != nil {
		return nil, err
	}
	full := filepath.Join(authDir, filename)
	raw := map[string]any{
		"type":           "codex",
		"provider":       ProviderOpenAI,
		"access_token":   tr.AccessToken,
		"refresh_token":  tr.RefreshToken,
		"id_token":       tr.IDToken,
		"email":          email,
		"account_id":     accountID,
		"plan_type":      planType,
		"expired":        expires.UTC().Format(time.RFC3339),
		"last_refresh":   time.Now().UTC().Format(time.RFC3339),
		"max_concurrent": maxConcurrent,
		"label":          label,
	}
	if sess.ProxyURL != "" {
		raw["proxy_url"] = sess.ProxyURL
	}
	if g := NormalizeGroup(group); g != "" {
		raw["group"] = g
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(full, out, 0600); err != nil {
		return nil, err
	}
	a, err := parseFile(full, out)
	if err != nil {
		return nil, fmt.Errorf("parse newly written codex file: %w", err)
	}
	log.Infof("codex oauth login: saved %s (email=%s plan=%s exp=%s)", a.ID, email, planType, expires.Format(time.RFC3339))
	return a, nil
}

// buildCodexCredentialFilename matches the CLIProxyAPI convention:
//
//	codex-{email}.json              — no plan
//	codex-{email}-{plan}.json       — non-team plans
//	codex-{acctHash}-{email}-team.json — team plan (account hash disambiguates
//	                                     the same email belonging to multiple
//	                                     team workspaces)
func buildCodexCredentialFilename(email, planType, accountID, sessionID string) string {
	plan := strings.ToLower(strings.TrimSpace(planType))
	// Strip non-alphanumerics from plan label for filesystem safety.
	clean := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return -1
	}, plan)

	safeEmail := strings.TrimSpace(email)
	safeEmail = strings.ReplaceAll(safeEmail, "/", "_")
	safeEmail = strings.ReplaceAll(safeEmail, "\\", "_")
	if safeEmail == "" {
		safeEmail = sessionID
	}

	var name string
	switch {
	case clean == "team" && accountID != "":
		acct := sha256.Sum256([]byte(accountID))
		acctHash := hex.EncodeToString(acct[:])[:8]
		name = "codex-" + acctHash + "-" + safeEmail + "-team"
	case clean != "":
		name = "codex-" + safeEmail + "-" + clean
	default:
		name = "codex-" + safeEmail
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		name += ".json"
	}
	return name
}
