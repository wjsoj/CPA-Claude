package server

import (
	"net/http"
	"testing"
	"time"
)

func TestParseClaudeUsageLimitBody(t *testing.T) {
	now := time.Now()
	future := now.Add(3 * time.Hour).Truncate(time.Second).Unix()

	cases := []struct {
		name      string
		body      string
		wantOK    bool
		wantInPast bool // true => parsed t should be before now (i.e. fallback or no-op)
	}{
		{
			name:   "explicit timestamp",
			body:   `{"type":"error","error":{"type":"rate_limit_error","message":"Claude AI usage limit reached|` + itoa(future) + `"}}`,
			wantOK: true,
		},
		{
			name:   "marker without pipe → fallback 1h",
			body:   `{"error":{"message":"Claude AI usage limit reached"}}`,
			wantOK: true,
		},
		{
			name:   "marker with empty timestamp → fallback 1h",
			body:   `{"error":{"message":"Claude AI usage limit reached|"}}`,
			wantOK: true,
		},
		{
			name:   "case-insensitive marker",
			body:   `{"error":{"message":"CLAUDE AI USAGE LIMIT REACHED|` + itoa(future) + `"}}`,
			wantOK: true,
		},
		{
			name:   "stale timestamp falls back to 1h future",
			body:   `{"error":{"message":"Claude AI usage limit reached|1000000000"}}`,
			wantOK: true,
		},
		{
			name:   "no marker → not a usage limit",
			body:   `{"error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`,
			wantOK: false,
		},
		{
			name:   "extra-usage rejection is not a usage limit",
			body:   `{"error":{"message":"Extra usage is required to process this request"}}`,
			wantOK: false,
		},
		{
			name:   "empty body",
			body:   ``,
			wantOK: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ts, ok := parseClaudeUsageLimitBody([]byte(tc.body))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && !ts.After(time.Now()) {
				t.Fatalf("parsed reset %v should be in the future", ts)
			}
		})
	}
}

func TestIs429StealthBan(t *testing.T) {
	cases := []struct {
		name    string
		headers http.Header
		body    []byte
		want    bool
	}{
		{
			name:    "no headers, generic body → stealth",
			headers: http.Header{},
			body:    []byte(`{"error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`),
			want:    true,
		},
		{
			name:    "Retry-After present → not stealth",
			headers: http.Header{"Retry-After": []string{"30"}},
			body:    []byte(`{"error":{"message":"rate limited"}}`),
			want:    false,
		},
		{
			name: "anthropic-ratelimit-* present → not stealth",
			headers: http.Header{
				"Anthropic-Ratelimit-Requests-Remaining": []string{"0"},
			},
			body: []byte(`{"error":{"message":"rate limited"}}`),
			want: false,
		},
		{
			name: "ratelimit header lowercase key → not stealth (case-insensitive prefix match)",
			headers: http.Header{
				"anthropic-ratelimit-tokens-reset": []string{"2026-05-03T10:00:00Z"},
			},
			body: []byte(`{}`),
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := is429StealthBan(tc.headers, tc.body)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// itoa avoids pulling strconv into the test just for one cheap conversion.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
