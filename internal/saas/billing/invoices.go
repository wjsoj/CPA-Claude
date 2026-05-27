package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	ccauth "github.com/wjsoj/cc-core/auth"

	"github.com/wjsoj/CPA-Claude/internal/saas/db"
	"github.com/wjsoj/CPA-Claude/internal/saas/resend"
)

// InvoiceHandler hosts the invoice routes — split from the wallet Handler
// because the invoice surface has its own set of dependencies (PDF dir,
// resend client, title-suggest URL) that the wallet doesn't need.
type InvoiceHandler struct {
	DB     *db.DB
	Auth   TokenAuthFunc
	Resend *resend.Client

	PDFDir          string
	OpsEmail        string // who gets notified on each new request
	TitleSuggestURL string

	HTTP *http.Client // for the title-suggest proxy; defaults to a 5s-timeout client
}

// NewInvoiceHandler returns a fully wired invoice surface. Any nil
// dependency is treated as a degraded mode: nil Resend → log-only; PDFDir
// empty → only requests work, issuance can't write the PDF.
func NewInvoiceHandler(store *db.DB, auth TokenAuthFunc, rc *resend.Client, pdfDir, opsEmail, suggestURL string) *InvoiceHandler {
	if auth == nil {
		auth = func(c *gin.Context) string {
			v := strings.TrimSpace(c.GetHeader("Authorization"))
			if strings.HasPrefix(strings.ToLower(v), "bearer ") {
				return strings.TrimSpace(v[7:])
			}
			return ""
		}
	}
	return &InvoiceHandler{
		DB:              store,
		Auth:            auth,
		Resend:          rc,
		PDFDir:          pdfDir,
		OpsEmail:        opsEmail,
		TitleSuggestURL: suggestURL,
		// uTLS Chrome fingerprint — aiqicha.baidu.com is Cloudflare-fronted
		// and returns 200/empty-body to crypto/tls default ClientHello.
		HTTP: &http.Client{Transport: ccauth.NewPlainHTTPClient("", true).Transport, Timeout: 8 * time.Second},
	}
}

// UserRoutes mounts the per-token invoice surface under an auth'd group.
func (h *InvoiceHandler) UserRoutes(g *gin.RouterGroup) {
	g.GET("/invoice/summary", h.summary)
	g.GET("/invoice/titles", h.listTitles)
	g.POST("/invoice/titles", h.saveTitle)
	g.DELETE("/invoice/titles/:id", h.deleteTitle)
	g.GET("/invoice/title-suggest", h.titleSuggest)
	g.GET("/invoices", h.list)
	g.POST("/invoices", h.create)
	g.GET("/invoices/:id/download", h.download)
}

func (h *InvoiceHandler) requireToken(c *gin.Context) (string, bool) {
	tok := h.Auth(c)
	if tok == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		return "", false
	}
	return tok, true
}

func (h *InvoiceHandler) summary(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	s, err := h.DB.InvoiceableCNY(c.Request.Context(), tok)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"paid_cny":      round2(s.PaidCNY),
		"locked_cny":    round2(s.LockedCNY),
		"issued_cny":    round2(s.IssuedCNY),
		"available_cny": round2(s.AvailableCNY),
	})
}

func (h *InvoiceHandler) listTitles(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	q := c.Query("q")
	ts, err := h.DB.ListInvoiceTitles(c.Request.Context(), tok, q, 20)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(ts))
	for _, t := range ts {
		out = append(out, gin.H{
			"id":           t.ID,
			"name":         t.Name,
			"tax_no":       t.TaxNo,
			"address":      t.Address,
			"phone":        t.Phone,
			"bank":         t.Bank,
			"bank_account": t.BankAccount,
			"last_used_at": t.LastUsedAt.Unix(),
			"source":       "local",
		})
	}
	c.JSON(http.StatusOK, gin.H{"titles": out})
}

type titleBody struct {
	Name        string `json:"name"`
	TaxNo       string `json:"tax_no"`
	Address     string `json:"address"`
	Phone       string `json:"phone"`
	Bank        string `json:"bank"`
	BankAccount string `json:"bank_account"`
}

func (h *InvoiceHandler) saveTitle(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	var b titleBody
	if err := c.BindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if strings.TrimSpace(b.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name required"})
		return
	}
	if err := h.DB.UpsertInvoiceTitle(c.Request.Context(), db.InvoiceTitle{
		Token: tok, Name: b.Name, TaxNo: b.TaxNo, Address: b.Address,
		Phone: b.Phone, Bank: b.Bank, BankAccount: b.BankAccount,
	}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *InvoiceHandler) deleteTitle(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	if err := h.DB.DeleteInvoiceTitle(c.Request.Context(), tok, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// titleSuggest merges local-history matches with a best-effort remote
// lookup against TitleSuggestURL. Remote failures are logged and swallowed;
// the response always contains the local matches at minimum.
func (h *InvoiceHandler) titleSuggest(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	out := []gin.H{}
	seen := map[string]bool{}

	// Local matches first — they're authoritative, instantly returned.
	if local, err := h.DB.ListInvoiceTitles(c.Request.Context(), tok, q, 8); err == nil {
		for _, t := range local {
			key := strings.ToLower(t.Name)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, gin.H{
				"name":         t.Name,
				"tax_no":       t.TaxNo,
				"address":      t.Address,
				"phone":        t.Phone,
				"bank":         t.Bank,
				"bank_account": t.BankAccount,
				"source":       "local",
			})
		}
	}

	if q != "" && h.TitleSuggestURL != "" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 4*time.Second)
		defer cancel()
		if remote, err := h.fetchRemoteSuggest(ctx, q); err == nil {
			for _, r := range remote {
				name := strings.TrimSpace(r.Name)
				if name == "" {
					continue
				}
				key := strings.ToLower(name)
				if seen[key] {
					continue
				}
				seen[key] = true
				out = append(out, gin.H{
					"name":   name,
					"tax_no": r.TaxNo,
					"source": "remote",
				})
				if len(out) >= 20 {
					break
				}
			}
		} else {
			log.Debugf("invoice: title-suggest remote failed: %v", err)
		}
	}
	c.JSON(http.StatusOK, gin.H{"titles": out})
}

type remoteSuggestRow struct {
	Name  string
	TaxNo string
}

// fetchRemoteSuggest queries the configured company-name suggest endpoint.
// Defaults to 天眼查 (capi.tianyancha.com/cloud-tempest/search/suggest/v3),
// which is unauthenticated but IP-rate-limited: about a few hundred queries
// per day per source IP before {errorCode:302004,"请登录"}. We cache positive
// results for an hour to stretch that budget and silently fall back to
// local-history matches when the remote refuses.
func (h *InvoiceHandler) fetchRemoteSuggest(ctx context.Context, q string) ([]remoteSuggestRow, error) {
	if rows, ok := suggestCacheGet(q); ok {
		return rows, nil
	}
	bodyBytes, _ := json.Marshal(map[string]string{"keyword": q})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.TitleSuggestURL, strings.NewReader(string(bodyBytes)))
	req.Header.Set("Content-Type", "application/json")
	// Bare UA on purpose — tianyancha's bot detector flags Chrome-shaped
	// header combos on this endpoint; a generic UA passes more often.
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")

	httpClient := h.HTTP
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	// tianyancha: {state:"ok", errorCode:0, data:[{comName, taxCode, ...}]}
	// On rate-limit: {state:"error", errorCode:302004, data:{token:""}}
	var parsed struct {
		State     string          `json:"state"`
		ErrorCode int             `json:"errorCode"`
		Message   string          `json:"message"`
		Data      json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	if parsed.ErrorCode != 0 || parsed.State != "ok" {
		return nil, fmt.Errorf("remote: %s (code %d)", parsed.Message, parsed.ErrorCode)
	}
	var rows []map[string]any
	if err := json.Unmarshal(parsed.Data, &rows); err != nil {
		// data may also be {queryList:[…]} on alternative providers
		var alt struct {
			QueryList []map[string]any `json:"queryList"`
			Resultlst []map[string]any `json:"resultlst"`
		}
		if jerr := json.Unmarshal(parsed.Data, &alt); jerr != nil {
			return nil, err
		}
		rows = append(rows, alt.QueryList...)
		rows = append(rows, alt.Resultlst...)
	}
	out := make([]remoteSuggestRow, 0, len(rows))
	for _, r := range rows {
		name := pickStr(r, "comName", "resultStr", "entName", "name", "company")
		if name == "" {
			continue
		}
		out = append(out, remoteSuggestRow{
			Name:  stripHTML(name),
			TaxNo: pickStr(r, "taxCode", "creditCode", "regNo", "creditNo", "taxNo"),
		})
	}
	suggestCachePut(q, out)
	return out, nil
}

// suggestCache is a tiny in-process LRU-ish cache: positive results live
// for an hour, capped at 512 distinct keywords. Keeps tianyancha hits down
// when the same operator queries "北京大" three times in a row.
var (
	suggestCacheMu  sync.Mutex
	suggestCacheMap = map[string]suggestCacheEntry{}
)

type suggestCacheEntry struct {
	rows    []remoteSuggestRow
	expires time.Time
}

func suggestCacheGet(q string) ([]remoteSuggestRow, bool) {
	suggestCacheMu.Lock()
	defer suggestCacheMu.Unlock()
	e, ok := suggestCacheMap[q]
	if !ok || time.Now().After(e.expires) {
		return nil, false
	}
	return e.rows, true
}

func suggestCachePut(q string, rows []remoteSuggestRow) {
	suggestCacheMu.Lock()
	defer suggestCacheMu.Unlock()
	if len(suggestCacheMap) >= 512 {
		// Crude eviction: drop one arbitrary entry per overflow. The
		// hourly TTL bounds long-term growth anyway.
		for k := range suggestCacheMap {
			delete(suggestCacheMap, k)
			break
		}
	}
	suggestCacheMap[q] = suggestCacheEntry{rows: rows, expires: time.Now().Add(time.Hour)}
}

func pickStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

// stripHTML removes <em>…</em> highlight markers Aiqicha sprinkles in.
func stripHTML(s string) string {
	for _, t := range []string{"<em>", "</em>", "<br>", "<br/>"} {
		s = strings.ReplaceAll(s, t, "")
	}
	return strings.TrimSpace(s)
}

func (h *InvoiceHandler) list(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	invs, err := h.DB.ListInvoicesByToken(c.Request.Context(), tok, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	out := make([]gin.H, 0, len(invs))
	for _, v := range invs {
		out = append(out, invoiceUserView(v))
	}
	c.JSON(http.StatusOK, gin.H{"invoices": out})
}

type createInvoiceBody struct {
	CNYAmount    float64   `json:"cny_amount"`
	Title        titleBody `json:"title"`
	ContactEmail string    `json:"contact_email"`
}

func (h *InvoiceHandler) create(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	var b createInvoiceBody
	if err := c.BindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad request"})
		return
	}
	if b.CNYAmount <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cny_amount must be positive"})
		return
	}
	if !isLikelyEmail(b.ContactEmail) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "contact_email invalid"})
		return
	}
	if strings.TrimSpace(b.Title.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title.name required"})
		return
	}
	b.Title.TaxNo = strings.ToUpper(strings.TrimSpace(b.Title.TaxNo))
	if !isLikelyTaxNo(b.Title.TaxNo) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title.tax_no required (统一社会信用代码, 15-20 位字母数字)"})
		return
	}
	cny := round2(b.CNYAmount)

	inv, err := h.DB.CreateInvoice(c.Request.Context(), tok, cny, db.InvoiceTitle{
		Name: b.Title.Name, TaxNo: b.Title.TaxNo, Address: b.Title.Address,
		Phone: b.Title.Phone, Bank: b.Title.Bank, BankAccount: b.Title.BankAccount,
	}, strings.TrimSpace(b.ContactEmail))
	if err != nil {
		if errors.Is(err, db.ErrInsufficientInvoiceable) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Best-effort ops notification. Failure here doesn't fail the
	// request — the admin can still see it in the panel.
	go h.notifyOps(inv)

	c.JSON(http.StatusOK, invoiceUserView(inv))
}

func (h *InvoiceHandler) notifyOps(inv *db.Invoice) {
	if h.Resend == nil || h.OpsEmail == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Pull tax_no out of the frozen title snapshot so the operator can see
	// the 统一社会信用代码 without expanding the JSON block below.
	var snap struct {
		TaxNo string `json:"tax_no"`
	}
	_ = json.Unmarshal([]byte(inv.TitleSnapshot), &snap)
	subj := fmt.Sprintf("[CPA-Claude] 新发票申请 ¥%.2f — %s", inv.CNYAmount, inv.TitleName)
	body := fmt.Sprintf(
		"<p>用户申请了新的发票:</p>"+
			"<ul>"+
			"<li>发票编号: <b>#%d</b></li>"+
			"<li>金额: <b>¥%.2f</b></li>"+
			"<li>抬头: %s</li>"+
			"<li>统一社会信用代码: <code>%s</code></li>"+
			"<li>联系邮箱: %s</li>"+
			"<li>申请时间: %s</li>"+
			"<li>Token: <code>%s</code></li>"+
			"</ul>"+
			"<p>抬头快照:<br><pre>%s</pre></p>"+
			"<p>请到管理员面板 → Invoices 处理。</p>",
		inv.ID, inv.CNYAmount, inv.TitleName, snap.TaxNo, inv.ContactEmail,
		inv.CreatedAt.Format("2006-01-02 15:04:05"),
		maskToken(inv.Token), prettyJSON(inv.TitleSnapshot))
	_ = h.Resend.Send(ctx, resend.Email{
		To:      []string{h.OpsEmail},
		Subject: subj,
		HTML:    body,
		ReplyTo: inv.ContactEmail,
	})
}

func (h *InvoiceHandler) download(c *gin.Context) {
	tok, ok := h.requireToken(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bad id"})
		return
	}
	inv, err := h.DB.GetInvoice(c.Request.Context(), id)
	if err != nil || inv.Token != tok {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if inv.Status != db.InvoiceIssued || inv.PDFPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "invoice not issued yet"})
		return
	}
	if _, err := os.Stat(inv.PDFPath); err != nil {
		log.Warnf("invoice download: pdf missing on disk id=%d path=%s: %v", id, inv.PDFPath, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "pdf file missing"})
		return
	}
	filename := fmt.Sprintf("invoice-%d.pdf", inv.ID)
	c.Header("Content-Type", "application/pdf")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	c.File(inv.PDFPath)
}

// invoiceUserView is the JSON shape returned to the end user. Excludes
// internal-only fields like pdf_path; the download URL is the only way
// the client touches the file.
func invoiceUserView(v *db.Invoice) gin.H {
	out := gin.H{
		"id":            v.ID,
		"cny_amount":    v.CNYAmount,
		"title_name":    v.TitleName,
		"contact_email": v.ContactEmail,
		"status":        v.Status,
		"note":          v.Note,
		"created_at":    v.CreatedAt.Unix(),
		"title":         parseJSONMap(v.TitleSnapshot),
	}
	if !v.IssuedAt.IsZero() {
		out["issued_at"] = v.IssuedAt.Unix()
		out["downloadable"] = v.PDFPath != ""
	}
	if !v.RejectedAt.IsZero() {
		out["rejected_at"] = v.RejectedAt.Unix()
	}
	return out
}

func parseJSONMap(s string) map[string]any {
	if strings.TrimSpace(s) == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return map[string]any{}
	}
	return m
}

func prettyJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return s
	}
	return string(b)
}

// isLikelyTaxNo validates 统一社会信用代码: 15-20 chars, uppercase letters + digits.
// 18 is the post-2015 standard length; 15 covers legacy 税务登记号. Loose range so
// HK/foreign-invested-entity edge cases still pass.
func isLikelyTaxNo(s string) bool {
	if len(s) < 15 || len(s) > 20 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

func isLikelyEmail(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 5 || len(s) > 254 {
		return false
	}
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	if strings.IndexByte(s[at+1:], '.') < 0 {
		return false
	}
	return true
}

// PDFFilePath returns the on-disk path admin should use to save the PDF
// for invoice id. Caller is responsible for MkdirAll + writing the file.
func (h *InvoiceHandler) PDFFilePath(id int64) string {
	return filepath.Join(h.PDFDir, fmt.Sprintf("invoice-%d.pdf", id))
}
