package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/wjsoj/cc-core/auth"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

func TestCodexModelCapacityDoesNotFreezeCredential(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"The selected model is at capacity"}}`))
	}))
	defer upstream.Close()

	cred := &auth.Auth{
		ID:          "codex-capacity.json",
		Kind:        auth.KindOAuth,
		Provider:    auth.ProviderOpenAI,
		AccessToken: "token",
		AccountID:   "account",
		BaseURL:     upstream.URL,
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	s := &Server{
		cfg:  &config.Config{ChatGPTBackendBaseURL: upstream.URL},
		pool: auth.NewPool([]*auth.Auth{cred}, nil, 10*time.Minute, false, ""),
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body := []byte(`{"model":"gpt-5.6-luna","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	retry, done := s.doForwardCodexOAuth(c, cred, "/v1/responses", body, true,
		"gpt-5.6-luna", "client-token", "client", time.Now(), 1)
	if !retry || done {
		t.Fatalf("capacity error should retry this request only; got retry=%v done=%v", retry, done)
	}
	if cred.IsQuotaExceeded(time.Now()) {
		t.Fatal("model capacity error must not mark account quota")
	}
	healthy, hardFailure, reason, _ := cred.HealthSnapshot()
	if !healthy || hardFailure {
		t.Fatalf("model capacity error must leave credential healthy; healthy=%v hardFailure=%v reason=%q", healthy, hardFailure, reason)
	}
}

func TestGenuineCodexQuotaStillRemovesCredentialFromScheduler(t *testing.T) {
	cred := &auth.Auth{
		ID:       "codex-quota.json",
		Kind:     auth.KindOAuth,
		Provider: auth.ProviderOpenAI,
	}
	pool := auth.NewPool([]*auth.Auth{cred}, nil, 10*time.Minute, false, "")
	cred.MarkUsageLimitReached(time.Now().Add(time.Hour))

	if got := pool.Acquire(t.Context(), auth.ProviderOpenAI, "client", "", "gpt-5.6-luna", "session"); got != nil {
		t.Fatalf("genuine account quota must remain excluded from scheduling; got %s", got.ID)
	}
}
