package auth

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

// transportPool caches http.RoundTripper per proxy URL so distinct OAuth files
// can each have their own upstream proxy without exploding connections.
type transportPool struct {
	mu   sync.Mutex
	pool map[string]http.RoundTripper
}

var globalPool = &transportPool{pool: make(map[string]http.RoundTripper)}

// ClientFor returns an *http.Client that dials api.anthropic.com with the
// Chrome uTLS fingerprint via the given proxyURL (empty = direct).
// Clients are cached per (proxyURL).
func ClientFor(proxyURL string, useUTLS bool) *http.Client {
	globalPool.mu.Lock()
	defer globalPool.mu.Unlock()
	key := proxyURL
	if !useUTLS {
		key = "plain::" + proxyURL
	}
	if rt, ok := globalPool.pool[key]; ok {
		return &http.Client{Transport: rt, Timeout: 0}
	}
	var rt http.RoundTripper
	if useUTLS {
		rt = newUTLSTransport(proxyURL)
	} else {
		rt = newStdTransport(proxyURL)
	}
	globalPool.pool[key] = rt
	return &http.Client{Transport: rt, Timeout: 0}
}

// NewPlainHTTPClient builds a fresh *http.Client per call with no cross-request
// connection reuse — mirrors CLIProxyAPI's sdk/proxyutil pattern. Use this for
// hosts that proved unreliable under the shared/pooled transport in ClientFor
// (notably chatgpt.com/backend-api/codex), where stale h2 reuse surfaces as
// spurious "connection reset by peer". SOCKS5/HTTP(S) proxies are honored.
//
// If useUTLS is true, a fresh utlsTransport (Chrome_Auto fingerprint) is used
// — required for chatgpt.com where Cloudflare JA3/JA4-fingerprints crypto/tls
// default ClientHello and returns 403.
func NewPlainHTTPClient(proxyURL string, useUTLS bool) *http.Client {
	if useUTLS {
		return &http.Client{Transport: newUTLSTransport(proxyURL)}
	}
	tr, _ := http.DefaultTransport.(*http.Transport)
	if tr == nil {
		tr = &http.Transport{}
	} else {
		tr = tr.Clone()
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			scheme := strings.ToLower(u.Scheme)
			if scheme == "socks5" || scheme == "socks5h" {
				tr.Proxy = nil
				if dc := socks5DialContext(u); dc != nil {
					tr.DialContext = dc
				}
			} else {
				tr.Proxy = http.ProxyURL(u)
			}
		}
	}
	return &http.Client{Transport: tr}
}

func newStdTransport(proxyURL string) http.RoundTripper {
	tr := &http.Transport{
		ForceAttemptHTTP2: true,
		// Prune idle connections aggressively. The transport is globally
		// cached per proxy URL, so any stale connection held here gets
		// reused by the *next* request and explodes with
		// "read: connection reset by peer" if the remote or proxy quietly
		// dropped it. 30s is short enough that most backends' keep-alive
		// windows (chatgpt.com is ~60s, Anthropic ~60s) don't end up
		// holding zombie sockets against us.
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err == nil {
			scheme := strings.ToLower(u.Scheme)
			if scheme == "socks5" || scheme == "socks5h" {
				// http.Transport.Proxy does not support SOCKS; use DialContext instead.
				if dc := socks5DialContext(u); dc != nil {
					tr.DialContext = dc
				}
			} else {
				tr.Proxy = http.ProxyURL(u)
			}
		}
	}
	// Turn on HTTP/2 PING-based health checks. Without this, a stale h2
	// connection (common behind long-lived SOCKS5 tunnels or behind a
	// backend that silently drops idle streams) isn't noticed until the
	// next request tries to write on it and fails with "connection reset
	// by peer". ReadIdleTimeout fires a PING after N seconds of silence
	// and PingTimeout closes the connection if the PING doesn't come back
	// — the transport then re-dials for the in-flight request.
	if h2, err := http2.ConfigureTransports(tr); err == nil && h2 != nil {
		h2.ReadIdleTimeout = 30 * time.Second
		h2.PingTimeout = 15 * time.Second
	}
	return tr
}

// socks5DialContext returns a DialContext func that routes TCP dials through
// the given SOCKS5 proxy. Returns nil if the dialer can't be built.
func socks5DialContext(u *url.URL) func(context.Context, string, string) (net.Conn, error) {
	var auth *proxy.Auth
	if u.User != nil {
		pwd, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pwd}
	}
	d, err := proxy.SOCKS5("tcp", u.Host, auth, &net.Dialer{Timeout: 30 * time.Second})
	if err != nil {
		return nil
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// proxy.SOCKS5 does not honor ctx natively; wrap with a goroutine so
		// ctx cancellation at least interrupts the wait.
		type result struct {
			c   net.Conn
			err error
		}
		ch := make(chan result, 1)
		go func() {
			c, err := d.Dial(network, addr)
			ch <- result{c, err}
		}()
		select {
		case r := <-ch:
			return r.c, r.err
		case <-ctx.Done():
			go func() {
				if r := <-ch; r.c != nil {
					_ = r.c.Close()
				}
			}()
			return nil, ctx.Err()
		}
	}
}

// utlsTransport implements http.RoundTripper using uTLS Chrome fingerprint.
//
// ALPN is negotiated honestly: we offer both "h2" and "http/1.1" and dispatch
// on what the server actually picked. The earlier code hardcoded NextProtos to
// ["h2"] and unconditionally fed the conn into http2.Transport — when a relay
// vendor's edge spoke HTTP/1.1 (TLS handshake completes without ALPN match,
// then the server replies with "HTTP/1.1 200 OK..."), the http2 reader saw
// ASCII bytes and surfaced "frame header looked like an HTTP/1.1 header".
//
// h2 connections are pooled per addr; h1 fallbacks are single-shot (rare path,
// only triggers for third-party relays that don't speak h2).
type utlsTransport struct {
	proxyURL string
	mu       sync.Mutex
	h2Conns  map[string]*http2.ClientConn
	h2Tr     *http2.Transport
}

func newUTLSTransport(proxyURL string) *utlsTransport {
	return &utlsTransport{
		proxyURL: proxyURL,
		h2Conns:  make(map[string]*http2.ClientConn),
		// PING-based health checks: stale h2 conns (idle SOCKS5 tunnels,
		// silent stream drops) get noticed before the next RoundTrip writes
		// on a half-dead socket. Mirrors what newStdTransport does via
		// http2.ConfigureTransports.
		h2Tr: &http2.Transport{
			ReadIdleTimeout: 30 * time.Second,
			PingTimeout:     15 * time.Second,
		},
	}
}

func (t *utlsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	t.mu.Lock()
	if h2, ok := t.h2Conns[addr]; ok {
		if h2.CanTakeNewRequest() {
			t.mu.Unlock()
			resp, err := h2.RoundTrip(req)
			if err != nil {
				t.mu.Lock()
				if c, exists := t.h2Conns[addr]; exists && c == h2 {
					delete(t.h2Conns, addr)
				}
				t.mu.Unlock()
				return nil, err
			}
			return resp, nil
		}
		delete(t.h2Conns, addr)
	}
	t.mu.Unlock()

	uc, err := t.dialTLS(req.Context(), host, addr)
	if err != nil {
		return nil, err
	}
	proto := uc.ConnectionState().NegotiatedProtocol
	if proto == "h2" {
		h2, err := t.h2Tr.NewClientConn(uc)
		if err != nil {
			_ = uc.Close()
			return nil, err
		}
		t.mu.Lock()
		t.h2Conns[addr] = h2
		t.mu.Unlock()
		return h2.RoundTrip(req)
	}
	// ALPN agreed on http/1.1 (or no ALPN — most servers then default to h1
	// over TLS). One-shot: write request, read response, conn closes when
	// the body Close()s. No pooling, since this path is rare and pooling h1
	// over utls correctly (with proper Connection: keep-alive bookkeeping,
	// CONNECT-style proxies, and 100-continue) is more code than it's worth.
	return roundTripHTTP1(uc, req)
}

func (t *utlsTransport) dialTLS(ctx context.Context, host, addr string) (*utls.UConn, error) {
	var rawConn net.Conn
	var err error
	if t.proxyURL != "" {
		rawConn, err = dialViaProxy(ctx, t.proxyURL, addr)
	} else {
		d := &net.Dialer{Timeout: 30 * time.Second}
		rawConn, err = d.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	tlsCfg := &utls.Config{ServerName: host, NextProtos: []string{"h2", "http/1.1"}}
	uc := utls.UClient(rawConn, tlsCfg, utls.HelloChrome_Auto)
	if err := uc.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("utls handshake %s: %w", host, err)
	}
	return uc, nil
}

// roundTripHTTP1 sends one request over an already-handshaken utls conn and
// returns the response. The conn is owned by the response body — closing the
// body closes the conn. We force Connection: close so the server doesn't try
// to keep it alive (we wouldn't pool it either way).
func roundTripHTTP1(uc *utls.UConn, req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Set("Connection", "close")
	if err := r.Write(uc); err != nil {
		_ = uc.Close()
		return nil, err
	}
	resp, err := http.ReadResponse(bufio.NewReader(uc), r)
	if err != nil {
		_ = uc.Close()
		return nil, err
	}
	resp.Body = &connBoundBody{ReadCloser: resp.Body, conn: uc}
	return resp, nil
}

// connBoundBody ties the lifetime of an underlying net.Conn to the response
// body. Used by the HTTP/1.1 utls fallback path.
type connBoundBody struct {
	io.ReadCloser
	conn   net.Conn
	closed bool
	mu     sync.Mutex
}

func (b *connBoundBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	err := b.ReadCloser.Close()
	if cerr := b.conn.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

// dialViaProxy supports http:// and socks5:// proxies for HTTPS CONNECT.
func dialViaProxy(ctx context.Context, proxyURL, targetAddr string) (net.Conn, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 30 * time.Second}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		proxyAddr := u.Host
		if !strings.Contains(proxyAddr, ":") {
			if u.Scheme == "https" {
				proxyAddr += ":443"
			} else {
				proxyAddr += ":80"
			}
		}
		var conn net.Conn
		conn, err = d.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, err
		}
		if u.Scheme == "https" {
			tlsc := tls.Client(conn, &tls.Config{ServerName: u.Hostname()})
			if err := tlsc.HandshakeContext(ctx); err != nil {
				_ = conn.Close()
				return nil, err
			}
			conn = tlsc
		}
		req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", targetAddr, targetAddr)
		if u.User != nil {
			pwd, _ := u.User.Password()
			auth := u.User.Username() + ":" + pwd
			req += "Proxy-Authorization: Basic " + basicAuth(auth) + "\r\n"
		}
		req += "\r\n"
		if _, err := conn.Write([]byte(req)); err != nil {
			_ = conn.Close()
			return nil, err
		}
		br := make([]byte, 4096)
		n, err := conn.Read(br)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		resp := string(br[:n])
		if !strings.Contains(resp, " 200 ") {
			_ = conn.Close()
			return nil, fmt.Errorf("proxy CONNECT failed: %s", strings.SplitN(resp, "\r\n", 2)[0])
		}
		return conn, nil
	case "socks5", "socks5h":
		dc := socks5DialContext(u)
		if dc == nil {
			return nil, fmt.Errorf("failed to build socks5 dialer for %s", proxyURL)
		}
		return dc(ctx, "tcp", targetAddr)
	default:
		return nil, fmt.Errorf("unknown proxy scheme: %s", u.Scheme)
	}
}

func basicAuth(up string) string {
	const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	src := []byte(up)
	out := make([]byte, 0, (len(src)+2)/3*4)
	for i := 0; i < len(src); i += 3 {
		var b [3]byte
		n := copy(b[:], src[i:])
		v := uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2])
		out = append(out, b64[(v>>18)&0x3f], b64[(v>>12)&0x3f])
		if n > 1 {
			out = append(out, b64[(v>>6)&0x3f])
		} else {
			out = append(out, '=')
		}
		if n > 2 {
			out = append(out, b64[v&0x3f])
		} else {
			out = append(out, '=')
		}
	}
	return string(out)
}
