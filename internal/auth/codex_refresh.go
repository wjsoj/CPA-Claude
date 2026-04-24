package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// OpenAI (Codex) OAuth constants. Mirrors the ChatGPT Codex CLI — see
// CLIProxyAPI/internal/auth/codex/openai_auth.go for provenance.
const (
	openaiTokenURL = "https://auth.openai.com/oauth/token"
	openaiClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

// refreshCodexLocked refreshes an OpenAI/ChatGPT OAuth access token. Also
// reparses the returned id_token so plan_type / account_id / email stay in
// sync with whatever the upstream reports now (subscription tier can change
// between refreshes). Caller must hold a.refreshMu.
func (a *Auth) refreshCodexLocked(ctx context.Context, useUTLS bool) error {
	a.mu.RLock()
	refresh := a.RefreshToken
	a.mu.RUnlock()
	if refresh == "" {
		return fmt.Errorf("no refresh token")
	}

	data := url.Values{
		"client_id":     {openaiClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
		"scope":         {"openid profile email"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiTokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	client := ClientFor(a.ProxyURL, useUTLS)
	resp, err := client.Do(req)
	if err != nil {
		a.MarkFailure(fmt.Sprintf("codex refresh transport: %v", err))
		return fmt.Errorf("codex refresh %s: %w", a.ID, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		bodyStr := string(body)
		switch {
		case strings.Contains(strings.ToLower(bodyStr), "refresh_token_reused"):
			// Burned refresh token — terminal. OpenAI aggressively rotates and
			// considers a reused refresh as evidence of token leakage. Require
			// re-login before this credential is used again.
			a.MarkHardFailure(fmt.Sprintf("codex refresh_token reused: %s", bodyStr))
		case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusBadRequest && strings.Contains(bodyStr, "invalid_grant"):
			a.MarkHardFailure(fmt.Sprintf("codex refresh rejected (http %d): %s", resp.StatusCode, bodyStr))
		default:
			a.MarkFailure(fmt.Sprintf("codex refresh http %d", resp.StatusCode))
		}
		return fmt.Errorf("codex refresh %s: http %d: %s", a.ID, resp.StatusCode, bodyStr)
	}
	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tr); err != nil {
		a.MarkFailure(fmt.Sprintf("codex refresh parse: %v", err))
		return fmt.Errorf("codex refresh %s: parse: %w", a.ID, err)
	}
	// Re-parse ID token for fresh plan_type / account_id / email.
	var planType, accountID, email string
	if tr.IDToken != "" {
		if claims, perr := ParseCodexIDToken(tr.IDToken); perr == nil && claims != nil {
			planType = claims.PlanType()
			accountID = claims.AccountID()
			email = claims.Email
		}
	}
	a.mu.Lock()
	a.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		a.RefreshToken = tr.RefreshToken
	}
	if tr.IDToken != "" {
		a.IDToken = tr.IDToken
	}
	if tr.ExpiresIn > 0 {
		a.ExpiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	if planType != "" {
		a.PlanType = planType
	}
	if accountID != "" {
		a.AccountID = accountID
	}
	if email != "" && a.Email == "" {
		a.Email = email
	}
	a.mu.Unlock()
	a.MarkSuccess()
	if err := saveAuth(a); err != nil {
		log.Warnf("auth: persist refreshed codex token %s: %v", a.ID, err)
	} else {
		log.Infof("auth: refreshed codex %s (exp=%s plan=%s)", a.ID, a.ExpiresAt.Format(time.RFC3339), a.PlanType)
	}
	return nil
}
