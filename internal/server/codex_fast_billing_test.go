package server

import (
	"context"
	"math"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	saasdb "github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/cc-core/pricing"
	"github.com/wjsoj/cc-core/usage"
)

func TestCodexFastWalletMultiplierOnce(t *testing.T) {
	db, err := saasdb.Open(filepath.Join(t.TempDir(), "wallet.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.EnsureWallet(ctx, "fast-wallet"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddBalance(ctx, "fast-wallet", "topup", 10, "seed", "seed", false); err != nil {
		t.Fatal(err)
	}
	cred := codexHTTPTestOAuth("fast-wallet-account")
	s := codexHTTPTestServer("", cred)
	s.saas = &saasBilling{db: db}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	s.billCodexWSTurn(c, cred, "gpt-5.5", "fast-wallet", "tester", usage.Counts{InputTokens: 600, OutputTokens: 200, CacheReadTokens: 400, Requests: 1}, time.Second, pricing.CostOptions{ServiceTier: "priority", ResponseServiceTier: "default"})
	bal, err := db.GetBalance(ctx, "fast-wallet")
	if err != nil {
		t.Fatal(err)
	}
	// The credential is OAuth, i.e. a ChatGPT subscription, and
	// billCodexWSTurn derives CodexOAuth from the credential — so the
	// requested "priority" buys no premium and the base .0092 is what reaches
	// the wallet. This used to expect .023 (2.5x), charging the customer for
	// an upstream cost the subscription never incurred.
	want := 10 - .0092*saasdb.DefaultCodexMultiplier
	if math.Abs(bal-want) > 1e-8 {
		t.Fatalf("wallet %.8f want %.8f", bal, want)
	}
}
