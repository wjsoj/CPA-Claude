package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Browser-like headers used when the proxy server itself drives a Claude.com
// authorize page on behalf of a user-supplied session cookie. These mirror
// what a recent Chrome on Linux sends for a top-level navigation, paired with
// uTLS HelloChrome_Auto on the wire.
const (
	browserUA             = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	browserAccept         = "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8"
	browserAcceptLanguage = "en-US,en;q=0.9"
	browserAcceptEncoding = "gzip, deflate, br"
	browserSecChUA        = `"Chromium";v="131", "Not_A Brand";v="24", "Google Chrome";v="131"`
)

// LoginWithSessionCookie drives the OAuth authorize flow on the server using
// a Claude.com web session cookie (`sk-ant-sid02-…`). Steps:
//
//  1. Build the same /cai/oauth/authorize URL the CLI would.
//  2. GET it with cookie `sessionKey=<sid02>` over uTLS Chrome through
//     the user-supplied proxy. Redirects are NOT followed.
//  3. If the upstream returns 302 to the registered callback URI, extract
//     the one-time `code` + `state` and run the standard token-exchange
//     path. The resulting credential is persisted exactly like a normal
//     browser-login flow.
//  4. Any other response (200 HTML consent page, 401, 403, …) is reported
//     verbatim so the caller can advise the user.
//
// proxyURL is **required** — driving claude.com authorize from a server IP
// without one is reckless and we refuse it. useUTLS is forced on for the
// same reason: matching the JA3/JA4 of the browser session this cookie
// came from is the bare minimum for the request to look credible.
func LoginWithSessionCookie(
	ctx context.Context,
	sessionCookie, proxyURL, label, group string,
	maxConcurrent int,
	authDir string,
) (*Auth, error) {
	sessionCookie = strings.TrimSpace(sessionCookie)
	proxyURL = strings.TrimSpace(proxyURL)
	if sessionCookie == "" {
		return nil, fmt.Errorf("missing session cookie")
	}
	if !strings.HasPrefix(sessionCookie, "sk-ant-sid") {
		return nil, fmt.Errorf("session cookie must start with sk-ant-sid (claude.com sessionKey)")
	}
	if proxyURL == "" {
		return nil, fmt.Errorf("proxy_url is required for session-cookie login")
	}

	verifier, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return nil, err
	}
	authURL := buildAnthropicAuthURL(state, verifier)

	// Always uTLS for this path. If you change this to allow plain TLS,
	// re-read the function comment.
	client := ClientFor(proxyURL, true)
	noFollow := *client
	noFollow.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	code, redirectState, status, err := authorizeWithSession(ctx, &noFollow, authURL, sessionCookie)
	if err != nil {
		return nil, err
	}
	if redirectState != "" && redirectState != state {
		return nil, fmt.Errorf("authorize state mismatch (got %q want %q)", redirectState, state)
	}
	if code == "" {
		return nil, fmt.Errorf("authorize returned no code (http %d)", status)
	}

	// Synthesize a LoginSession so we can reuse finishAnthropicLogin verbatim.
	idBytes := make([]byte, 12)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, err
	}
	sess := &LoginSession{
		ID:           hex.EncodeToString(idBytes),
		Provider:     ProviderAnthropic,
		State:        state,
		CodeVerifier: verifier,
		ProxyURL:     proxyURL,
		Label:        strings.TrimSpace(label),
		CreatedAt:    time.Now(),
	}
	a, err := finishAnthropicLogin(ctx, sess, code, authDir, maxConcurrent, true, group)
	if err != nil {
		return nil, err
	}
	log.Infof("session-cookie login: success id=%s email=%s", a.ID, a.Email)
	return a, nil
}

// authorizeWithSession performs the GET <authorize URL> step with the
// provided sessionKey cookie and returns whatever code/state the server
// redirects to. Returns the HTTP status as a third value so the caller
// can surface a precise error message.
func authorizeWithSession(
	ctx context.Context,
	client *http.Client,
	authURL, sessionCookie string,
) (code, state string, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	if err != nil {
		return "", "", 0, err
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept", browserAccept)
	req.Header.Set("Accept-Language", browserAcceptLanguage)
	req.Header.Set("Accept-Encoding", browserAcceptEncoding)
	req.Header.Set("Sec-Ch-Ua", browserSecChUA)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Linux"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	req.Header.Set("Cookie", "sessionKey="+sessionCookie)

	resp, err := client.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("authorize GET: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	status = resp.StatusCode

	if status >= 300 && status < 400 {
		loc := resp.Header.Get("Location")
		if loc == "" {
			return "", "", status, fmt.Errorf("authorize %d but no Location header", status)
		}
		// Location is usually absolute (http://localhost:54545/callback?…)
		// but tolerate relative just in case.
		u, perr := url.Parse(loc)
		if perr != nil {
			return "", "", status, fmt.Errorf("parse Location %q: %w", loc, perr)
		}
		q := u.Query()
		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			if desc == "" {
				return "", "", status, fmt.Errorf("authorize error: %s", e)
			}
			return "", "", status, fmt.Errorf("authorize error: %s (%s)", e, desc)
		}
		return q.Get("code"), q.Get("state"), status, nil
	}

	// Non-redirect: surface what we can. 200 = consent page, 401/403 = bad
	// session or CF challenge. Read a small snippet of the body to help the
	// user diagnose.
	snippet := make([]byte, 512)
	n, _ := io.ReadFull(resp.Body, snippet)
	hint := strings.TrimSpace(string(snippet[:n]))
	if hint != "" && len(hint) > 240 {
		hint = hint[:240] + "…"
	}
	switch status {
	case http.StatusOK:
		return "", "", status, fmt.Errorf("authorize returned 200 (consent page) — this account hasn't pre-approved Claude Code in a browser yet, or claude.com served an interactive page. Sign in to claude.ai once and run `claude` to approve, then retry")
	case http.StatusUnauthorized, http.StatusForbidden:
		return "", "", status, fmt.Errorf("authorize http %d — session cookie rejected (expired or Cloudflare challenge). body: %s", status, hint)
	default:
		return "", "", status, fmt.Errorf("authorize http %d — unexpected. body: %s", status, hint)
	}
}
