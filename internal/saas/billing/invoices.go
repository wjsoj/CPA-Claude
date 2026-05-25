package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// fetchRemoteSuggest hits Baidu Aiqicha's open suggestion endpoint. The
// response shape is documented inline because nobody else does.
func (h *InvoiceHandler) fetchRemoteSuggest(ctx context.Context, q string) ([]remoteSuggestRow, error) {
	u, err := url.Parse(h.TitleSuggestURL)
	if err != nil {
		return nil, err
	}
	qs := u.Query()
	qs.Set("q", q)
	qs.Set("t", strconv.FormatInt(time.Now().UnixMilli(), 10))
	u.RawQuery = qs.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	// Real-browser headers — these endpoints frequently 403 on a bare
	// Go-http client.
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://aiqicha.baidu.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")

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
	// Aiqicha returns {status, msg, data: {queryList: [{resultStr, ...}]}}
	// We treat the parse loosely — any "name"/"company"/"entName" field
	// that surfaces is fine.
	var parsed struct {
		Data struct {
			QueryList []map[string]any `json:"queryList"`
			Resultlst []map[string]any `json:"resultlst"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	rows := append([]map[string]any{}, parsed.Data.QueryList...)
	rows = append(rows, parsed.Data.Resultlst...)
	var out []remoteSuggestRow
	for _, r := range rows {
		name := pickStr(r, "resultStr", "entName", "name", "company")
		if name == "" {
			continue
		}
		out = append(out, remoteSuggestRow{
			Name:  stripHTML(name),
			TaxNo: pickStr(r, "regNo", "creditNo", "taxNo"),
		})
	}
	return out, nil
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
	subj := fmt.Sprintf("[CPA-Claude] 新发票申请 ¥%.2f — %s", inv.CNYAmount, inv.TitleName)
	body := fmt.Sprintf(
		"<p>用户申请了新的发票:</p>"+
			"<ul>"+
			"<li>发票编号: <b>#%d</b></li>"+
			"<li>金额: <b>¥%.2f</b></li>"+
			"<li>抬头: %s</li>"+
			"<li>联系邮箱: %s</li>"+
			"<li>申请时间: %s</li>"+
			"<li>Token: <code>%s</code></li>"+
			"</ul>"+
			"<p>抬头快照:<br><pre>%s</pre></p>"+
			"<p>请到管理员面板 → Invoices 处理。</p>",
		inv.ID, inv.CNYAmount, inv.TitleName, inv.ContactEmail,
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
