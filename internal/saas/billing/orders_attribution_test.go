package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// A pool top-up is filed under the admin who paid for it, so it appears in his
// personal order list while the credit went to the workspace — his wallet
// balance never moves and no wallet_tx row is written. The order row is the
// only place that difference can be seen, so it has to carry the workspace, or
// the page shows a paid order whose money went nowhere.
func TestPoolTopupOrderCarriesItsWorkspace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	d, err := db.Open(filepath.Join(t.TempDir(), "orders.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx := context.Background()

	const admin = "sk-admin-token-for-order-attribution"
	ws, err := d.CreateWorkspace(ctx, "科研组")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.AddMember(ctx, ws.ID, admin, db.WSRoleAdmin, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := d.EnsureWallet(ctx, admin); err != nil {
		t.Fatal(err)
	}

	// One of each, filed under the same token — which is exactly the case the
	// attribution exists to disambiguate.
	for _, o := range []db.AlipayOrder{
		{OutTradeNo: "personal-1", Token: admin, CNYAmount: 72, USDCredit: 10, Rate: 7.2},
		{OutTradeNo: "pool-1", Token: admin, WorkspaceID: ws.ID, CNYAmount: 144, USDCredit: 20, Rate: 7.2},
	} {
		if err := d.CreateOrder(ctx, o); err != nil {
			t.Fatalf("seed %s: %v", o.OutTradeNo, err)
		}
	}

	h := &Handler{DB: d, Auth: func(c *gin.Context) string { return c.GetHeader("X-Tok") }}
	e := gin.New()
	e.GET("/api/wallet/orders", h.orders)

	req := httptest.NewRequest("GET", "/api/wallet/orders", nil)
	req.Header.Set("X-Tok", admin)
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s)", w.Code, w.Body)
	}

	var got struct {
		Orders []map[string]any `json:"orders"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byNo := map[string]map[string]any{}
	for _, o := range got.Orders {
		no, _ := o["out_trade_no"].(string)
		byNo[no] = o
	}
	if len(byNo) != 2 {
		t.Fatalf("orders = %d, want 2 (%s)", len(byNo), w.Body)
	}

	pool := byNo["pool-1"]
	if pool["workspace_id"] == nil {
		t.Error("pool top-up carries no workspace_id — the page cannot tell it apart from a personal one")
	}
	if name, _ := pool["workspace_name"].(string); name != "科研组" {
		t.Errorf("workspace_name = %q, want %q", name, "科研组")
	}

	// The personal order must stay unmarked; labelling everything is the same
	// failure with the opposite sign.
	if personal := byNo["personal-1"]; personal["workspace_id"] != nil {
		t.Errorf("personal top-up carries workspace_id = %v, want absent", personal["workspace_id"])
	}
}
