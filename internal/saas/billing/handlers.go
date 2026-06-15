package billing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
)

// Gateway abstracts a payment gateway (Z-Pay, MockGateway). Implementations
// differ in signing scheme and the shape of the payment surface handed back;
// every gateway funnels through the same applyNotification path so the
// credit-side validation is uniform.
type Gateway interface {
	CreatePayment(ctx context.Context, p PayParams) (*PayResult, error)
	VerifyNotify(req map[string][]string) (*Notification, error)
	QueryTrade(ctx context.Context, outTradeNo string) (*Notification, error)
	AppID() string
}

// PayParams is the input to Gateway.CreatePayment. Gateways may ignore
// fields they don't use.
type PayParams struct {
	OutTradeNo string
	Subject    string
	TotalCNY   float64
	Method     string // currently always "alipay"
	ClientIP   string // some aggregators (Z-Pay mapi.php) require it
}

// PayResult is the union of payment surfaces a gateway may return. Frontend
// prefers PayURL (browser redirect) when set, falling back to QRCode
// rendered to a QR image, falling back to Img (a hosted QR PNG).
type PayResult struct {
	QRCode string `json:"qr_code,omitempty"`
	PayURL string `json:"pay_url,omitempty"`
	Img    string `json:"img,omitempty"`
}

// Notification is the verified subset of gateway notify / sync query fields
// the credit path consumes.
type Notification struct {
	OutTradeNo  string
	TradeNo     string
	TradeStatus string
	TotalAmount string // raw, e.g. "68.28"
	AppID       string
	SellerID    string
}

// TokenAuthFunc returns the verified client token for the current request
// (after the caller's middleware has authenticated it). Returning "" means
// the request is unauthenticated and the handler must reject it.
type TokenAuthFunc func(c *gin.Context) string

type Handler struct {
	DB      *db.DB
	Rate    *Rate
	Gateway Gateway
	Site    string
	Auth    TokenAuthFunc

	OrderTTL          time.Duration
	MaxPendingPerUser int

	mu        sync.Mutex
	createdAt map[string][]time.Time // per-token sliding 1h window
}

func NewHandler(store *db.DB, rate *Rate, gw Gateway, site string, auth TokenAuthFunc) *Handler {
	if auth == nil {
		auth = func(c *gin.Context) string {
			v := strings.TrimSpace(c.GetHeader("Authorization"))
			if strings.HasPrefix(strings.ToLower(v), "bearer ") {
				return strings.TrimSpace(v[7:])
			}
			return ""
		}
	}
	return &Handler{
		DB: store, Rate: rate, Gateway: gw, Site: site, Auth: auth,
		// 24h: a pending order stays creditable for a full day so a slow
		// payment + late gateway notify still lands. A short TTL (the old
		// 15 min) sweeps the order row before a delayed Z-Pay notify
		// arrives; the late notify then hits applyNotification's "order not
		// found" branch and the payment is silently dropped (real money
		// paid, never credited). The Z-Pay QR going stale in ~10-15 min only
		// blocks *new* payments on that code — it doesn't justify discarding
		// an order a buyer may still pay against within the day.
		OrderTTL:          24 * time.Hour,
		MaxPendingPerUser: 5,
		createdAt:         make(map[string][]time.Time),
	}
}

// UserRoutes mounts wallet endpoints under an authenticated group (the
// caller is responsible for installing auth middleware that populates the
// token-resolver h.Auth reads from).
func (h *Handler) UserRoutes(g *gin.RouterGroup) {
	g.GET("/balance", h.balance)
	g.GET("/transactions", h.transactions)
	g.GET("/orders", h.orders)
	g.GET("/orders/:id", h.orderStatus)
	g.DELETE("/orders/:id", h.orderCancel)
	g.POST("/topup", h.topup)
}

// PublicRoutes mounts the gateway-side endpoints that need no auth (their
// payload is signed by the gateway, not by the client).
func (h *Handler) PublicRoutes(g *gin.RouterGroup) {
	g.GET("/rate", h.exchangeRate)
	g.POST("/notify", h.notify)
	g.GET("/notify", h.notify) // Z-Pay delivers notify as GET
}

func (h *Handler) requireToken(c *gin.Context) (string, bool) {
	tok := h.Auth(c)
	if tok == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		return "", false
	}
	return tok, true
}

func (h *Handler) balance(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	w, err := h.DB.EnsureWallet(c.Request.Context(), tok)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	g, _ := h.DB.GetGroup(c.Request.Context(), w.GroupID)
	resp := gin.H{
		"balance_usd": w.BalanceUSD,
		"group_id":    w.GroupID,
	}
	if g != nil {
		resp["group_name"] = g.Name
		resp["claude_multiplier"] = g.ClaudeMultiplier
		resp["codex_multiplier"] = g.CodexMultiplier
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) exchangeRate(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"cny_per_usd": h.Rate.CNYPerUSD(),
		"as_of":       h.Rate.AsOf().Unix(),
	})
}

func (h *Handler) transactions(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	// Fetch a wider window because we filter out per-request 'charge'
	// rows here — they dominate the ledger by volume and aren't useful
	// to end users. The status page only shows topups + admin adjustments
	// + refunds; per-request consumption is summarised elsewhere.
	txs, err := h.DB.ListWalletTx(c.Request.Context(), tok, 1000)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(txs))
	for _, t := range txs {
		if t.Kind == db.TxKindCharge {
			continue
		}
		out = append(out, gin.H{
			"id":         t.ID,
			"kind":       t.Kind,
			"amount_usd": t.AmountUSD,
			"ref":        t.Ref,
			"note":       t.Note,
			"created_at": t.CreatedAt.Unix(),
		})
	}
	c.JSON(http.StatusOK, gin.H{"transactions": out})
}

func (h *Handler) orders(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	os, err := h.DB.ListOrders(c.Request.Context(), tok, 100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(os))
	for _, o := range os {
		out = append(out, orderView(o))
	}
	c.JSON(http.StatusOK, gin.H{"orders": out})
}

type topupReq struct {
	USD float64 `json:"usd"`
}

func (h *Handler) topup(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	var req topupReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if math.IsInf(req.USD, 0) || math.IsNaN(req.USD) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid amount"})
		return
	}
	if req.USD < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "min top-up is $1"})
		return
	}
	if req.USD > 1000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "max top-up is $1000"})
		return
	}
	if okRate, retry := h.allowCreate(tok); !okRate {
		c.Header("Retry-After", strconv.Itoa(retry))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many top-up requests", "retry_after": retry})
		return
	}
	if pending, _ := h.DB.CountPendingOrders(c.Request.Context(), tok); pending >= h.MaxPendingPerUser {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":          "too many pending orders",
			"pending_orders": pending,
			"max_pending":    h.MaxPendingPerUser,
			"hint":           "wait for existing orders to expire or close them via the Billing page",
		})
		return
	}
	if _, err := h.DB.EnsureWallet(c.Request.Context(), tok); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	rate := h.Rate.CNYPerUSD()
	cny := round2(req.USD * rate)
	out, err := genOutTradeNo(tok)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	subject := fmt.Sprintf("%s wallet top-up: $%.2f", h.Site, req.USD)
	pr, err := h.Gateway.CreatePayment(c.Request.Context(), PayParams{
		OutTradeNo: out, Subject: subject, TotalCNY: cny,
		Method: "alipay", ClientIP: c.ClientIP(),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "gateway: " + err.Error()})
		return
	}
	// Pack the three surfaces into qr_code as JSON so we don't need a
	// schema migration to add pay_url/img alongside it.
	stored, _ := json.Marshal(pr)
	if err := h.DB.CreateOrder(c.Request.Context(), db.AlipayOrder{
		OutTradeNo: out, Token: tok, CNYAmount: cny, USDCredit: req.USD,
		Rate: rate, QRCode: string(stored),
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if mock, ok := h.Gateway.(*MockGateway); ok {
		go mock.AutoConfirm(h, out)
	}
	c.JSON(http.StatusOK, gin.H{
		"out_trade_no": out,
		"cny_amount":   cny,
		"usd_credit":   req.USD,
		"rate":         rate,
		"method":       "alipay",
		"qr_code":      pr.QRCode,
		"pay_url":      pr.PayURL,
		"img":          pr.Img,
	})
}

func (h *Handler) orderStatus(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	out := c.Param("id")
	o, err := h.DB.GetOrder(c.Request.Context(), out)
	if err != nil || o.Token != tok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, orderView(o))
}

// orderCancel lets a user drop one of their own pending orders — e.g.
// they changed their mind before paying. Only pending orders are
// cancellable; paid orders are immutable financial state, and expired
// rows the sweeper already removes. Ownership is enforced by the
// DELETE WHERE clause matching both out_trade_no and token, so the
// endpoint can't be coaxed into deleting another user's row even with
// a spoofed out_trade_no.
func (h *Handler) orderCancel(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	out := c.Param("id")
	if err := h.DB.DeletePendingOrder(c.Request.Context(), out, tok); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found or not pending"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

func (h *Handler) notify(c *gin.Context) {
	if err := c.Request.ParseForm(); err != nil {
		c.String(http.StatusBadRequest, "fail")
		return
	}
	n, err := h.Gateway.VerifyNotify(c.Request.Form)
	if err != nil {
		log.Warnf("billing notify: verify failed: %v", err)
		c.String(http.StatusBadRequest, "fail")
		return
	}
	if err := h.applyNotification(c.Request.Context(), n, "gateway-notify"); err != nil {
		// Terminal: ACK to stop retries. Transient: 500 so the gateway
		// retries until the DB is back.
		if errors.Is(err, ErrOrderTampered) || errors.Is(err, ErrOrderExpired) || errors.Is(err, ErrOrderUnknown) {
			log.Warnf("billing notify: rejected (terminal): out=%s reason=%v", n.OutTradeNo, err)
			c.String(http.StatusOK, "success")
			return
		}
		log.Warnf("billing notify: transient credit failure for %s: %v", n.OutTradeNo, err)
		c.String(http.StatusInternalServerError, "fail")
		return
	}
	c.String(http.StatusOK, "success")
}

// applyNotification is the single funnel through which an order can be
// credited — both the async notify path and the admin reconciliation path
// call this. Validates trade_status, app_id, total_amount, ttl, then
// atomically marks paid + credits the wallet.
func (h *Handler) applyNotification(ctx context.Context, n *Notification, source string) error {
	o, err := h.DB.GetOrder(ctx, n.OutTradeNo)
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return ErrOrderUnknown
		}
		return err
	}
	if o.Status == db.OrderPaid {
		if o.TradeNo != "" && n.TradeNo != "" && o.TradeNo != n.TradeNo {
			log.Warnf("billing %s: trade_no mismatch on already-paid order %s (have=%s, got=%s)", source, o.OutTradeNo, o.TradeNo, n.TradeNo)
		}
		return nil
	}
	if o.Status == db.OrderExpired || o.Status == db.OrderFailed {
		return ErrOrderExpired
	}
	if !strings.EqualFold(n.TradeStatus, "TRADE_SUCCESS") && !strings.EqualFold(n.TradeStatus, "TRADE_FINISHED") {
		return fmt.Errorf("trade_status %q not settled", n.TradeStatus)
	}
	if want := h.Gateway.AppID(); want != "" && n.AppID != "" && n.AppID != want {
		return fmt.Errorf("%w: app_id mismatch (got=%s want=%s)", ErrOrderTampered, n.AppID, want)
	}
	got, perr := strconv.ParseFloat(strings.TrimSpace(n.TotalAmount), 64)
	if perr != nil {
		return fmt.Errorf("%w: bad total_amount %q", ErrOrderTampered, n.TotalAmount)
	}
	if !sameMoney(got, o.CNYAmount) {
		return fmt.Errorf("%w: amount mismatch (got=%.2f want=%.2f)", ErrOrderTampered, got, o.CNYAmount)
	}
	if h.OrderTTL > 0 && time.Since(o.CreatedAt) > h.OrderTTL {
		_ = h.DB.MarkOrderExpired(ctx, o.OutTradeNo)
		return ErrOrderExpired
	}
	note := fmt.Sprintf("zpay top-up ¥%.2f @ %.4f (%s)", o.CNYAmount, o.Rate, source)
	if _, err := h.DB.CreditPaidOrder(ctx, o.OutTradeNo, n.TradeNo, o.Token, o.USDCredit, o.OutTradeNo, note); err != nil {
		if errors.Is(err, db.ErrOrderNotPending) {
			return nil
		}
		return err
	}
	log.Infof("billing [%s]: credited token=%s order=%s usd=%.2f cny=%.2f trade_no=%s",
		source, maskToken(o.Token), o.OutTradeNo, o.USDCredit, o.CNYAmount, n.TradeNo)
	return nil
}

// Sentinel errors. Terminal failures end the retry loop.
var (
	ErrOrderUnknown  = errors.New("order not found")
	ErrOrderExpired  = errors.New("order expired or closed")
	ErrOrderTampered = errors.New("order validation failed")
)

func sameMoney(a, b float64) bool {
	const epsilon = 0.005 // half a fen
	return math.Abs(a-b) < epsilon
}

func orderView(o *db.AlipayOrder) gin.H {
	paid := int64(0)
	if !o.PaidAt.IsZero() {
		paid = o.PaidAt.Unix()
	}
	var pr PayResult
	if s := strings.TrimSpace(o.QRCode); strings.HasPrefix(s, "{") {
		_ = json.Unmarshal([]byte(s), &pr)
	} else {
		pr.QRCode = s
	}
	return gin.H{
		"out_trade_no": o.OutTradeNo,
		"cny_amount":   o.CNYAmount,
		"usd_credit":   o.USDCredit,
		"rate":         o.Rate,
		"status":       o.Status,
		"trade_no":     o.TradeNo,
		"qr_code":      pr.QRCode,
		"pay_url":      pr.PayURL,
		"img":          pr.Img,
		"created_at":   o.CreatedAt.Unix(),
		"paid_at":      paid,
	}
}

// allowCreate gates per-token order creation: max 10 / hour.
func (h *Handler) allowCreate(token string) (bool, int) {
	const maxPerHour = 10
	now := time.Now()
	cutoff := now.Add(-time.Hour)
	h.mu.Lock()
	defer h.mu.Unlock()
	stamps := h.createdAt[token]
	kept := stamps[:0]
	for _, t := range stamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= maxPerHour {
		retry := int(kept[0].Add(time.Hour).Sub(now).Seconds())
		if retry < 1 {
			retry = 1
		}
		h.createdAt[token] = kept
		return false, retry
	}
	h.createdAt[token] = append(kept, now)
	return true, 0
}

// RunExpirySweeper periodically marks pending orders older than OrderTTL
// as expired. Cheap — a single UPDATE.
func (h *Handler) RunExpirySweeper(ctx context.Context) {
	if h.OrderTTL <= 0 {
		return
	}
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cutoff := time.Now().Add(-h.OrderTTL)
			n, err := h.DB.ExpirePendingOrdersBefore(ctx, cutoff)
			if err != nil {
				log.Warnf("billing: expiry sweep failed: %v", err)
				continue
			}
			if n > 0 {
				log.Infof("billing: expired %d stale pending order(s) older than %s", n, h.OrderTTL)
			}
		}
	}
}

// ReconcileOrder is the admin-triggered repair path: query the gateway for
// out_trade_no and apply the result through applyNotification. Used when an
// async notify never arrived (firewall, network blip) but the user did pay.
func (h *Handler) ReconcileOrder(ctx context.Context, outTradeNo string) (string, error) {
	n, err := h.Gateway.QueryTrade(ctx, outTradeNo)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(n.TradeStatus, "TRADE_SUCCESS") && !strings.EqualFold(n.TradeStatus, "TRADE_FINISHED") {
		return n.TradeStatus, nil
	}
	if err := h.applyNotification(ctx, n, "admin-reconcile"); err != nil {
		return "", err
	}
	return "credited", nil
}

func genOutTradeNo(token string) (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	// Prefix CPA + an 8-char hash of the token (so order IDs aren't tied to
	// internal IDs and the same token always sorts together) + timestamp +
	// random tail.
	tokHash := tokenShort(token)
	return fmt.Sprintf("CPA%s%s%s", tokHash, time.Now().Format("20060102150405"), hex.EncodeToString(b)[:6]), nil
}

func tokenShort(token string) string {
	// 8 hex chars derived from the token. Stable, non-reversible without a
	// rainbow table, and the cardinality is more than enough for ordering.
	b := make([]byte, 0, 64)
	for i := 0; i < len(token); i++ {
		b = append(b, token[i])
	}
	h := uint64(1469598103934665603) // FNV-1a 64-bit
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return fmt.Sprintf("%08x", uint32(h))
}

func maskToken(tok string) string {
	if len(tok) <= 10 {
		return "***"
	}
	return tok[:6] + "…" + tok[len(tok)-4:]
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// --- Gateways ----------------------------------------------------------

// MockGateway is the dev-mode stand-in: returns a fake QR URL and
// synthesizes its own notify after a short delay. The synthetic notify
// goes through the same applyNotification path so amount/app_id checks
// still run.
type MockGateway struct{}

const MockAppID = "mock-app-id"

func (g *MockGateway) CreatePayment(_ context.Context, p PayParams) (*PayResult, error) {
	return &PayResult{QRCode: "https://example.com/mock-pay-qr/" + p.OutTradeNo}, nil
}
func (g *MockGateway) VerifyNotify(_ map[string][]string) (*Notification, error) {
	return nil, errors.New("mock gateway does not receive notifications")
}
func (g *MockGateway) QueryTrade(_ context.Context, _ string) (*Notification, error) {
	return nil, errors.New("mock gateway does not support query")
}
func (g *MockGateway) AppID() string { return MockAppID }

func (g *MockGateway) AutoConfirm(h *Handler, outTradeNo string) {
	time.Sleep(2 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	o, err := h.DB.GetOrder(ctx, outTradeNo)
	if err != nil {
		log.Warnf("mock-gateway: get order: %v", err)
		return
	}
	n := &Notification{
		OutTradeNo:  o.OutTradeNo,
		TradeNo:     "MOCK-" + o.OutTradeNo,
		TradeStatus: "TRADE_SUCCESS",
		TotalAmount: fmt.Sprintf("%.2f", o.CNYAmount),
		AppID:       MockAppID,
	}
	if err := h.applyNotification(ctx, n, "mock-gateway"); err != nil {
		log.Warnf("mock-gateway: apply: %v", err)
	}
}
