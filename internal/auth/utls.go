package auth

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
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

func newStdTransport(proxyURL string) http.RoundTripper {
	tr := &http.Transport{
		ForceAttemptHTTP2:     true,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	if proxyURL != "" {
		if u, err := url.Parse(proxyURL); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	return tr
}

// utlsTransport implements http.RoundTripper using uTLS Chrome fingerprint.
type utlsTransport struct {
	proxyURL string
	mu       sync.Mutex
	conns    map[string]*http2.ClientConn
}

func newUTLSTransport(proxyURL string) *utlsTransport {
	return &utlsTransport{proxyURL: proxyURL, conns: make(map[string]*http2.ClientConn)}
}

func (t *utlsTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(host, port)

	t.mu.Lock()
	h2, ok := t.conns[addr]
	if ok && h2.CanTakeNewRequest() {
		t.mu.Unlock()
		resp, err := h2.RoundTrip(req)
		if err != nil {
			t.mu.Lock()
			if c, exists := t.conns[addr]; exists && c == h2 {
				delete(t.conns, addr)
			}
			t.mu.Unlock()
			return nil, err
		}
		return resp, nil
	}
	t.mu.Unlock()

	h2, err := t.dial(req.Context(), host, addr)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.conns[addr] = h2
	t.mu.Unlock()
	return h2.RoundTrip(req)
}

func (t *utlsTransport) dial(ctx context.Context, host, addr string) (*http2.ClientConn, error) {
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
	tlsCfg := &utls.Config{ServerName: host, NextProtos: []string{"h2"}}
	uc := utls.UClient(rawConn, tlsCfg, utls.HelloChrome_Auto)
	if err := uc.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("utls handshake %s: %w", host, err)
	}
	tr := &http2.Transport{}
	h2, err := tr.NewClientConn(uc)
	if err != nil {
		_ = uc.Close()
		return nil, err
	}
	return h2, nil
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
		return nil, fmt.Errorf("socks5 not supported yet (use http/https proxy)")
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
