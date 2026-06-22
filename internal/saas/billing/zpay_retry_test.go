package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// newTestGateway points a ZPayGateway at an arbitrary base URL but keeps the
// production HTTP client (short dial timeout + retry-friendly transport).
func newTestGateway(t *testing.T, base string) *ZPayGateway {
	t.Helper()
	g, err := NewZPayGateway(ZPayParams{BaseURL: base, PID: "1000", Key: "secret"})
	if err != nil {
		t.Fatalf("NewZPayGateway: %v", err)
	}
	return g
}

// TestCreatePaymentRetriesThenSucceeds verifies a transient failure on the
// first /mapi.php hit is retried and the second (healthy) response credits a
// payment surface. Simulates the "one dead CDN IP" scenario by failing the
// first attempt with a hijack-close (connection reset).
func TestCreatePaymentRetriesThenSucceeds(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			// Abort the connection mid-flight → transport error, like a dead IP.
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Errorf("server does not support hijacking")
				return
			}
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"msg":"ok","payurl":"https://qr.alipay.com/abc","trade_no":"T123"}`))
	}))
	defer srv.Close()

	g := newTestGateway(t, srv.URL)
	res, err := g.CreatePayment(context.Background(), PayParams{
		OutTradeNo: "CPA-test-1", Subject: "test", TotalCNY: 6.77, Method: "alipay", ClientIP: "1.2.3.4",
	})
	if err != nil {
		t.Fatalf("CreatePayment after retry: %v", err)
	}
	if res.PayURL != "https://qr.alipay.com/abc" {
		t.Fatalf("unexpected pay url: %q", res.PayURL)
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected 2 attempts (1 fail + 1 success), got %d", got)
	}
}

// TestCreatePaymentBusinessErrorNotRetried verifies a well-formed HTTP 200 with
// a non-success business code is surfaced immediately, NOT retried (retrying a
// gateway reject is pointless and would mask the real error).
func TestCreatePaymentBusinessErrorNotRetried(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"merchant disabled"}`))
	}))
	defer srv.Close()

	g := newTestGateway(t, srv.URL)
	_, err := g.CreatePayment(context.Background(), PayParams{
		OutTradeNo: "CPA-test-2", Subject: "test", TotalCNY: 6.77, Method: "alipay", ClientIP: "1.2.3.4",
	})
	if err == nil {
		t.Fatal("expected business error, got nil")
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("business rejects must not retry: expected 1 attempt, got %d", got)
	}
}

// TestCreatePaymentCallerCanceledStops verifies that if the caller's context is
// already canceled, no (further) retries are attempted.
func TestCreatePaymentCallerCanceledStops(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		_ = conn.Close()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled

	g := newTestGateway(t, srv.URL)
	_, err := g.CreatePayment(ctx, PayParams{
		OutTradeNo: "CPA-test-3", Subject: "test", TotalCNY: 6.77, Method: "alipay", ClientIP: "1.2.3.4",
	})
	if err == nil {
		t.Fatal("expected error with canceled context")
	}
	// At most one attempt should have been made before bailing on ctx.Err().
	if got := atomic.LoadInt32(&hits); got > 1 {
		t.Fatalf("canceled caller must not drive retries: got %d attempts", got)
	}
}

// TestNewZPayHTTPClientTimeouts sanity-checks the tuned timeouts are wired so a
// regression that drops them back to the old 15s single-shot is caught.
func TestNewZPayHTTPClientTimeouts(t *testing.T) {
	c := newZPayHTTPClient()
	if c.Timeout != 12*time.Second {
		t.Fatalf("client timeout = %v, want 12s", c.Timeout)
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.TLSHandshakeTimeout != 4*time.Second {
		t.Fatalf("TLS handshake timeout = %v, want 4s", tr.TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout != 8*time.Second {
		t.Fatalf("response header timeout = %v, want 8s", tr.ResponseHeaderTimeout)
	}
}
