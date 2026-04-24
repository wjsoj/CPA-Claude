package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Anthropic OAuth constants (Claude Code CLI client). Redirect URI is
// fixed by Anthropic's OAuth app registration; we cannot change it.
const (
	anthropicAuthURL     = "https://claude.ai/oauth/authorize"
	anthropicRedirectURI = "http://localhost:54545/callback"
	anthropicScopes      = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
)

// LoginSession holds the short-lived state for a browser-initiated OAuth flow.
// Provider selects between the Anthropic (claude.ai) and OpenAI (ChatGPT
// Codex) endpoints, since the two differ in authorize URL, scopes, client
// id, redirect uri, token-exchange body shape, and the JWT claims parsed
// out of the response.
type LoginSession struct {
	ID           string
	Provider     string
	State        string
	CodeVerifier string
	ProxyURL     string
	Label        string
	CreatedAt    time.Time
}

// loginStore tracks in-flight login sessions by ID. They expire after 30 min.
type loginStore struct {
	mu       sync.Mutex
	sessions map[string]*LoginSession
}

var globalLoginStore = &loginStore{sessions: make(map[string]*LoginSession)}

func (s *loginStore) gc() {
	cutoff := time.Now().Add(-30 * time.Minute)
	for k, v := range s.sessions {
		if v.CreatedAt.Before(cutoff) {
			delete(s.sessions, k)
		}
	}
}

func (s *loginStore) put(sess *LoginSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gc()
	s.sessions[sess.ID] = sess
}

func (s *loginStore) take(id string) *LoginSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gc()
	v := s.sessions[id]
	delete(s.sessions, id)
	return v
}

// ---- PKCE helpers ----

func randomURLSafe(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func pkceChallenge(verifier string) string {
	h := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(h[:])
}

// StartLogin creates a new login session for the given provider and returns
// the browser URL the user must visit. proxyURL (optional) is used for the
// token-exchange call at FinishLogin; it does not affect the user's
// browser navigation.
func StartLogin(provider, proxyURL, label string) (*LoginSession, string, error) {
	provider = NormalizeProvider(provider)
	// 96 bytes → 128-char base64url verifier. Matches upstream CLIProxyAPI
	// and the vendor CLIs on both providers.
	verifier, err := randomURLSafe(96)
	if err != nil {
		return nil, "", err
	}
	state, err := randomURLSafe(24)
	if err != nil {
		return nil, "", err
	}
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, "", err
	}
	sess := &LoginSession{
		ID:           hex.EncodeToString(idBytes),
		Provider:     provider,
		State:        state,
		CodeVerifier: verifier,
		ProxyURL:     strings.TrimSpace(proxyURL),
		Label:        strings.TrimSpace(label),
		CreatedAt:    time.Now(),
	}
	globalLoginStore.put(sess)

	var authURL string
	switch provider {
	case ProviderOpenAI:
		authURL = buildCodexAuthURL(state, verifier)
	default:
		authURL = buildAnthropicAuthURL(state, verifier)
	}
	return sess, authURL, nil
}

func buildAnthropicAuthURL(state, verifier string) string {
	params := url.Values{
		"code":                  {"true"},
		"client_id":             {anthropicClientID},
		"response_type":         {"code"},
		"redirect_uri":          {anthropicRedirectURI},
		"scope":                 {anthropicScopes},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}
	return anthropicAuthURL + "?" + params.Encode()
}

// RedirectURIFor returns the redirect URI a given provider will send the
// user back to after the consent screen. Admin UI surfaces this so users
// can set up an SSH tunnel on remote installs.
func RedirectURIFor(provider string) string {
	switch NormalizeProvider(provider) {
	case ProviderOpenAI:
		return openaiRedirectURI
	default:
		return anthropicRedirectURI
	}
}

// ParseCallback extracts code+state from any of: a raw `code#state` string
// (as shown on Claude's manual-copy page), a `code=...&state=...` fragment,
// or a full redirect URL (`http://localhost:54545/callback?code=...&state=...`).
func ParseCallback(input string) (code, state string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty callback")
	}
	// Full URL?
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		u, err := url.Parse(input)
		if err != nil {
			return "", "", err
		}
		q := u.Query()
		if e := q.Get("error"); e != "" {
			return "", "", fmt.Errorf("oauth error: %s", e)
		}
		return q.Get("code"), q.Get("state"), nil
	}
	// "code#state" format (Claude sometimes presents this to the user on a
	// manual-copy fallback page).
	if strings.Contains(input, "#") {
		parts := strings.SplitN(input, "#", 2)
		return parts[0], parts[1], nil
	}
	// "code=...&state=..." query-string form without scheme.
	if strings.Contains(input, "=") {
		if vals, err := url.ParseQuery(input); err == nil {
			return vals.Get("code"), vals.Get("state"), nil
		}
	}
	// Last resort: treat the whole thing as the code. User must then paste
	// state separately via API.
	return input, "", nil
}

type exchangeResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Account      struct {
		UUID         string `json:"uuid"`
		EmailAddress string `json:"email_address"`
	} `json:"account"`
	Organization struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"organization"`
}

// FinishLogin exchanges the authorization code for tokens, writes the new
// credential to authDir, and returns the parsed Auth ready to add to a Pool.
// The exchange URL, client_id, redirect_uri, and response shape all come
// from the session's Provider — registered by StartLogin.
func FinishLogin(
	ctx context.Context,
	sessionID, code, state, authDir string,
	maxConcurrent int,
	useUTLS bool,
	group string,
) (*Auth, error) {
	sess := globalLoginStore.take(sessionID)
	if sess == nil {
		return nil, fmt.Errorf("unknown or expired login session")
	}
	if strings.TrimSpace(code) == "" {
		return nil, fmt.Errorf("missing code")
	}
	if state != "" && state != sess.State {
		return nil, fmt.Errorf("state mismatch")
	}

	switch NormalizeProvider(sess.Provider) {
	case ProviderOpenAI:
		return finishCodexLogin(ctx, sess, code, authDir, maxConcurrent, useUTLS, group)
	default:
		return finishAnthropicLogin(ctx, sess, code, authDir, maxConcurrent, useUTLS, group)
	}
}

// finishAnthropicLogin exchanges the code for Claude tokens and persists
// the resulting credential. Extracted from FinishLogin when the codex
// variant was added; behavior is unchanged.
func finishAnthropicLogin(
	ctx context.Context,
	sess *LoginSession,
	code, authDir string,
	maxConcurrent int,
	useUTLS bool,
	group string,
) (*Auth, error) {
	body := map[string]any{
		"code":          code,
		"state":         sess.State,
		"grant_type":    "authorization_code",
		"client_id":     anthropicClientID,
		"redirect_uri":  anthropicRedirectURI,
		"code_verifier": sess.CodeVerifier,
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicTokenURL, strings.NewReader(string(buf)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := ClientFor(sess.ProxyURL, useUTLS)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange http %d: %s", resp.StatusCode, string(data))
	}
	var tr exchangeResponse
	if err := json.Unmarshal(data, &tr); err != nil {
		return nil, fmt.Errorf("token exchange parse: %w", err)
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return nil, fmt.Errorf("token exchange returned empty tokens")
	}

	expires := time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	email := tr.Account.EmailAddress
	label := sess.Label
	if label == "" {
		label = email
	}
	filename := sanitizeLoginFilename("", email, sess.ID)

	if err := os.MkdirAll(authDir, 0700); err != nil {
		return nil, err
	}
	full := filepath.Join(authDir, filename)
	raw := map[string]any{
		"type":           "claude",
		"provider":       ProviderAnthropic,
		"access_token":   tr.AccessToken,
		"refresh_token":  tr.RefreshToken,
		"email":          email,
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
		return nil, fmt.Errorf("parse newly written file: %w", err)
	}
	log.Infof("oauth login: saved %s (email=%s exp=%s)", a.ID, email, expires.Format(time.RFC3339))
	return a, nil
}

// sanitizeLoginFilename builds an on-disk filename for a newly-persisted
// OAuth credential. `tag` is an optional prefix used on non-Anthropic
// providers (e.g. "codex") to keep the auths/ directory self-documenting.
func sanitizeLoginFilename(tag, email, sessionID string) string {
	s := strings.TrimSpace(email)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	s = strings.ReplaceAll(s, "..", "")
	if s == "" {
		if tag != "" {
			s = tag + "-" + sessionID
		} else {
			s = "claude-" + sessionID
		}
	} else if tag != "" {
		s = tag + "-" + s
	}
	if !strings.HasSuffix(strings.ToLower(s), ".json") {
		s += ".json"
	}
	return s
}
