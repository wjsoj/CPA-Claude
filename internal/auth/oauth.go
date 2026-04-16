package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// Anthropic OAuth constants.
const (
	anthropicTokenURL = "https://api.anthropic.com/v1/oauth/token"
	anthropicClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

// fileFormat is the JSON layout written by `claude setup-token` / our own
// login flow. We accept extra keys and preserve them on save.
type fileFormat struct {
	Type          string         `json:"type"`
	AccessToken   string         `json:"access_token"`
	RefreshToken  string         `json:"refresh_token"`
	Email         string         `json:"email,omitempty"`
	Expire        string         `json:"expired,omitempty"` // RFC3339 string
	ExpiresAt     int64          `json:"expires_at,omitempty"`
	ProxyURL      string         `json:"proxy_url,omitempty"`
	MaxConcurrent int            `json:"max_concurrent,omitempty"`
	Disabled      bool           `json:"disabled,omitempty"`
	Label         string         `json:"label,omitempty"`
	Extra         map[string]any `json:"-"`
}

func parseFile(path string, data []byte) (*Auth, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	t, _ := raw["type"].(string)
	kindStr := strings.ToLower(strings.TrimSpace(t))
	switch kindStr {
	case "claude":
		// OAuth credential (fall through to existing parse path).
	case "apikey", "api_key", "anthropic_api_key":
		return parseAPIKeyFile(path, raw)
	default:
		return nil, fmt.Errorf("unsupported type %q (expected claude or apikey)", t)
	}
	access, _ := raw["access_token"].(string)
	refresh, _ := raw["refresh_token"].(string)
	if access == "" && refresh == "" {
		return nil, fmt.Errorf("missing access_token and refresh_token")
	}
	email, _ := raw["email"].(string)
	label, _ := raw["label"].(string)
	if label == "" {
		label = email
	}
	if label == "" {
		label = filepath.Base(path)
	}
	disabled, _ := raw["disabled"].(bool)
	proxyURL, _ := raw["proxy_url"].(string)
	maxConc := 0
	if v, ok := raw["max_concurrent"].(float64); ok {
		maxConc = int(v)
	}

	exp := time.Time{}
	if s, ok := raw["expired"].(string); ok && s != "" {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			exp = t
		}
	}
	if exp.IsZero() {
		if v, ok := raw["expires_at"].(float64); ok {
			exp = time.Unix(int64(v), 0)
		}
	}

	a := &Auth{
		ID:            filepath.Base(path),
		Kind:          KindOAuth,
		Label:         label,
		Email:         email,
		AccessToken:   access,
		RefreshToken:  refresh,
		ExpiresAt:     exp,
		ProxyURL:      proxyURL,
		MaxConcurrent: maxConc,
		FilePath:      path,
		Disabled:      disabled,
	}
	return a, nil
}

func parseAPIKeyFile(path string, raw map[string]any) (*Auth, error) {
	apiKey, _ := raw["api_key"].(string)
	if apiKey == "" {
		// Tolerate "key" / "access_token" spellings.
		if s, ok := raw["key"].(string); ok {
			apiKey = s
		} else if s, ok := raw["access_token"].(string); ok {
			apiKey = s
		}
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("missing api_key")
	}
	label, _ := raw["label"].(string)
	if label == "" {
		label = filepath.Base(path)
	}
	disabled, _ := raw["disabled"].(bool)
	proxyURL, _ := raw["proxy_url"].(string)
	baseURL, _ := raw["base_url"].(string)
	return &Auth{
		ID:          filepath.Base(path),
		Kind:        KindAPIKey,
		Label:       label,
		AccessToken: apiKey,
		ProxyURL:    proxyURL,
		BaseURL:     baseURL,
		FilePath:    path,
		Disabled:    disabled,
	}, nil
}

// LoadAuthDir reads every *.json under dir and splits the parsed auths into
// OAuth and API-key slices. Deprecated alias LoadOAuthDir is preserved for
// callers that only expect OAuth.
func LoadAuthDir(dir string) (oauths, apikeys []*Auth, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, errRead := os.ReadFile(full)
		if errRead != nil {
			log.Warnf("auth: read %s: %v", full, errRead)
			continue
		}
		a, errParse := parseFile(full, data)
		if errParse != nil {
			log.Warnf("auth: parse %s: %v", full, errParse)
			continue
		}
		if a.Kind == KindAPIKey {
			apikeys = append(apikeys, a)
		} else {
			oauths = append(oauths, a)
		}
	}
	return oauths, apikeys, nil
}

// LoadOAuthDir retained for backward compatibility. Returns only OAuth auths.
func LoadOAuthDir(dir string) ([]*Auth, error) {
	oauths, _, err := LoadAuthDir(dir)
	return oauths, err
}

var saveMu sync.Mutex

// saveAuth atomically rewrites the OAuth file with fresh tokens plus
// admin-editable fields (disabled, max_concurrent, proxy_url), preserving
// any extra keys from the original file.
func saveAuth(a *Auth) error {
	saveMu.Lock()
	defer saveMu.Unlock()
	if a.FilePath == "" {
		return nil
	}
	var raw map[string]any
	if data, err := os.ReadFile(a.FilePath); err == nil {
		_ = json.Unmarshal(data, &raw)
	}
	if raw == nil {
		raw = make(map[string]any)
	}
	a.mu.RLock()
	if a.Kind == KindAPIKey {
		raw["type"] = "apikey"
		raw["api_key"] = a.AccessToken
		if a.BaseURL != "" {
			raw["base_url"] = a.BaseURL
		} else {
			delete(raw, "base_url")
		}
		// Clear OAuth-only keys if the file was converted.
		delete(raw, "refresh_token")
		delete(raw, "access_token")
		delete(raw, "expired")
		delete(raw, "id_token")
		delete(raw, "last_refresh")
		delete(raw, "max_concurrent")
	} else {
		raw["type"] = "claude"
		raw["access_token"] = a.AccessToken
		raw["refresh_token"] = a.RefreshToken
		if !a.ExpiresAt.IsZero() {
			raw["expired"] = a.ExpiresAt.UTC().Format(time.RFC3339)
		}
		raw["max_concurrent"] = a.MaxConcurrent
	}
	raw["disabled"] = a.Disabled
	if a.ProxyURL != "" {
		raw["proxy_url"] = a.ProxyURL
	} else {
		delete(raw, "proxy_url")
	}
	if a.Label != "" {
		raw["label"] = a.Label
	}
	a.mu.RUnlock()
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := a.FilePath + ".tmp"
	if err := os.WriteFile(tmp, out, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, a.FilePath)
}

// Persist writes the current admin-editable fields of the auth back to disk.
func (a *Auth) Persist() error { return saveAuth(a) }

// ParseFile is the exported variant of parseFile for admin upload handlers.
func ParseFile(path string, data []byte) (*Auth, error) { return parseFile(path, data) }

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Account      struct {
		EmailAddress string `json:"email_address"`
	} `json:"account"`
}

// needsRefresh reports whether the OAuth token is missing or within `leeway`
// of expiry. Returns false if there is no refresh token to use.
func (a *Auth) needsRefresh(leeway time.Duration) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.RefreshToken == "" {
		return false
	}
	if a.ExpiresAt.IsZero() {
		return true
	}
	return time.Until(a.ExpiresAt) < leeway
}

// EnsureFresh refreshes the access token if it's within `leeway` of expiry.
// Concurrent callers are deduplicated via a per-auth refresh mutex so the
// rotating refresh_token is never burned by parallel exchanges.
func (a *Auth) EnsureFresh(ctx context.Context, leeway time.Duration, useUTLS bool) error {
	if !a.needsRefresh(leeway) {
		return nil
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	// Double-check: another goroutine may have refreshed while we waited.
	if !a.needsRefresh(leeway) {
		return nil
	}
	return a.doRefreshLocked(ctx, useUTLS)
}

// doRefreshLocked performs the HTTP exchange. Caller must hold a.refreshMu.
func (a *Auth) doRefreshLocked(ctx context.Context, useUTLS bool) error {
	a.mu.RLock()
	refresh := a.RefreshToken
	a.mu.RUnlock()
	if refresh == "" {
		return fmt.Errorf("no refresh token")
	}

	body := map[string]any{
		"client_id":     anthropicClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refresh,
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicTokenURL, strings.NewReader(string(buf)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	client := ClientFor(a.ProxyURL, useUTLS)
	resp, err := client.Do(req)
	if err != nil {
		a.MarkFailure(fmt.Sprintf("refresh transport: %v", err))
		return fmt.Errorf("oauth refresh %s: %w", a.ID, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		bodyStr := string(data)
		var errResp struct {
			Error            string `json:"error"`
			ErrorDescription string `json:"error_description"`
		}
		_ = json.Unmarshal(data, &errResp)
		switch {
		case resp.StatusCode == http.StatusBadRequest && errResp.Error == "invalid_grant":
			// Refresh token revoked / not found / already used. Terminal —
			// requires manual re-login. Mark hard so the picker stops handing
			// this auth out and the admin panel surfaces it.
			a.MarkHardFailure(fmt.Sprintf("refresh_token revoked (invalid_grant): %s", errResp.ErrorDescription))
		case resp.StatusCode == http.StatusUnauthorized:
			a.MarkHardFailure(fmt.Sprintf("refresh unauthorized (http 401): %s", bodyStr))
		default:
			a.MarkFailure(fmt.Sprintf("refresh http %d", resp.StatusCode))
		}
		return fmt.Errorf("oauth refresh %s: http %d: %s", a.ID, resp.StatusCode, bodyStr)
	}
	var tr refreshResponse
	if err := json.Unmarshal(data, &tr); err != nil {
		a.MarkFailure(fmt.Sprintf("refresh parse: %v", err))
		return fmt.Errorf("oauth refresh %s: parse: %w", a.ID, err)
	}
	a.mu.Lock()
	a.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		a.RefreshToken = tr.RefreshToken
	}
	if tr.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	if a.Email == "" && tr.Account.EmailAddress != "" {
		a.Email = tr.Account.EmailAddress
	}
	a.mu.Unlock()
	// Successful refresh implicitly clears any prior transient failure state
	// — the credential is demonstrably alive again.
	a.MarkSuccess()
	if err := saveAuth(a); err != nil {
		log.Warnf("auth: persist refreshed token %s: %v", a.ID, err)
	} else {
		log.Infof("auth: refreshed %s (exp=%s)", a.ID, a.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}
