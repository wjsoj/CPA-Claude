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
	if strings.ToLower(strings.TrimSpace(t)) != "claude" {
		return nil, fmt.Errorf("unsupported type %q (expected claude)", t)
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

// LoadOAuthDir reads every *.json under dir and returns valid Claude auths.
func LoadOAuthDir(dir string) ([]*Auth, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Auth
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(full)
		if err != nil {
			log.Warnf("auth: read %s: %v", full, err)
			continue
		}
		a, err := parseFile(full, data)
		if err != nil {
			log.Warnf("auth: parse %s: %v", full, err)
			continue
		}
		out = append(out, a)
	}
	return out, nil
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
	raw["type"] = "claude"
	a.mu.RLock()
	raw["access_token"] = a.AccessToken
	raw["refresh_token"] = a.RefreshToken
	if !a.ExpiresAt.IsZero() {
		raw["expired"] = a.ExpiresAt.UTC().Format(time.RFC3339)
	}
	raw["disabled"] = a.Disabled
	raw["max_concurrent"] = a.MaxConcurrent
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

// EnsureFresh refreshes the access token if it's within `leeway` of expiry.
// Safe for concurrent callers — deduplicates refresh attempts per auth.
func (a *Auth) EnsureFresh(ctx context.Context, leeway time.Duration, useUTLS bool) error {
	a.mu.RLock()
	needs := !a.ExpiresAt.IsZero() && time.Until(a.ExpiresAt) < leeway && a.RefreshToken != ""
	a.mu.RUnlock()
	if !needs {
		return nil
	}
	return a.refresh(ctx, useUTLS)
}

func (a *Auth) refresh(ctx context.Context, useUTLS bool) error {
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
		return fmt.Errorf("oauth refresh %s: %w", a.ID, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("oauth refresh %s: http %d: %s", a.ID, resp.StatusCode, string(data))
	}
	var tr refreshResponse
	if err := json.Unmarshal(data, &tr); err != nil {
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
	if err := saveAuth(a); err != nil {
		log.Warnf("auth: persist refreshed token %s: %v", a.ID, err)
	} else {
		log.Infof("auth: refreshed %s (exp=%s)", a.ID, a.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}
