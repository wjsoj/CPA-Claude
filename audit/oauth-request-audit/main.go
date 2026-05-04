package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type row struct {
	Idx        int               `json:"idx"`
	StartTime  int64             `json:"startTime"`
	URL        string            `json:"url"`
	Method     string            `json:"method"`
	StatusCode any               `json:"statusCode"`
	ReqHeaders map[string]any    `json:"reqHeaders"`
	ReqSize    any               `json:"reqSize"`
	ReqBody    string            `json:"reqBody"`
	ResHeaders map[string]any    `json:"resHeaders"`
	ResBody    string            `json:"resBody"`
	Raw        map[string]any    `json:"-"`
	BodyObject map[string]any    `json:"-"`
	BodyError  string            `json:"-"`
	HeaderStr  map[string]string `json:"-"`
}

type finding struct {
	Area     string
	Severity string
	Evidence string
	Why      string
}

func main() {
	root := flag.String("root", ".", "repository root")
	out := flag.String("out", "audit/oauth-request-audit/reports/oauth-request-audit.md", "report path")
	flag.Parse()

	rows, err := loadRows(filepath.Join(*root, "crack", "oauth", "rows"))
	if err != nil {
		fatal(err)
	}
	sources, err := loadSourceSignals(*root)
	if err != nil {
		fatal(err)
	}
	report := buildReport(rows, sources)
	if err := os.MkdirAll(filepath.Dir(*out), 0755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*out, []byte(report), 0644); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %s\n", *out)
}

func loadRows(dir string) ([]row, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []row
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || e.Name() == "_manifest.json" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var r row
		if err := json.Unmarshal(data, &r); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		r.Raw = raw
		r.HeaderStr = normalizeHeaders(r.ReqHeaders)
		if strings.HasPrefix(strings.TrimSpace(r.ReqBody), "{") {
			var body map[string]any
			if err := json.Unmarshal([]byte(r.ReqBody), &body); err != nil {
				r.BodyError = err.Error()
			} else {
				r.BodyObject = body
			}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Idx < out[j].Idx })
	return out, nil
}

func normalizeHeaders(h map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		lk := strings.ToLower(k)
		switch x := v.(type) {
		case string:
			out[lk] = x
		case []any:
			parts := make([]string, 0, len(x))
			for _, it := range x {
				parts = append(parts, fmt.Sprint(it))
			}
			out[lk] = strings.Join(parts, " | ")
		default:
			out[lk] = fmt.Sprint(x)
		}
	}
	return out
}

func loadSourceSignals(root string) (map[string]string, error) {
	files := []string{
		"internal/server/fingerprint.go",
		"internal/server/mimicry.go",
		"internal/server/sidecar.go",
		"internal/server/proxy.go",
		"internal/auth/oauth.go",
	}
	out := map[string]string{}
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return nil, err
		}
		out[rel] = string(data)
	}
	return out, nil
}

func buildReport(rows []row, src map[string]string) string {
	var b strings.Builder
	b.WriteString("# OAuth Request Audit\n\n")
	b.WriteString("Generated: " + time.Now().Format(time.RFC3339) + "\n\n")
	b.WriteString("Scope: local static audit only. The tool reads `crack/oauth/rows` and selected Go source files; it does not contact upstream services.\n\n")

	writeGoldenFlow(&b, rows)
	writeFieldMatrix(&b, rows)
	writeSourceSignals(&b, src)
	writeFindings(&b, rows, src)
	writeNextSteps(&b)
	return b.String()
}

func writeGoldenFlow(b *strings.Builder, rows []row) {
	b.WriteString("## Golden OAuth Flow\n\n")
	b.WriteString("| idx | method | url | phase guess | request shape |\n")
	b.WriteString("|---:|---|---|---|---|\n")
	for _, r := range rows {
		phase := phaseGuess(r)
		shape := bodyShape(r)
		b.WriteString(fmt.Sprintf("| %d | %s | `%s` | %s | %s |\n", r.Idx, r.Method, trimURL(r.URL), phase, shape))
	}
	b.WriteString("\n")
}

func writeFieldMatrix(b *strings.Builder, rows []row) {
	b.WriteString("## Key Field Matrix\n\n")
	selected := []int{1, 2, 3, 4, 5, 6, 9, 10, 14, 16, 17}
	for _, idx := range selected {
		r, ok := findRow(rows, idx)
		if !ok {
			continue
		}
		b.WriteString(fmt.Sprintf("### Row %02d `%s %s`\n\n", r.Idx, r.Method, trimURL(r.URL)))
		b.WriteString("- Headers: " + headerSummary(r) + "\n")
		b.WriteString("- Body: " + bodySummary(r) + "\n")
		if uid := metadataUserID(r); uid != "" {
			b.WriteString("- `metadata.user_id`: " + uid + "\n")
		}
		b.WriteString("\n")
	}
}

func writeSourceSignals(b *strings.Builder, src map[string]string) {
	b.WriteString("## Current Implementation Signals\n\n")
	fp := src["internal/server/fingerprint.go"]
	sidecar := src["internal/server/sidecar.go"]
	mimicry := src["internal/server/mimicry.go"]
	proxy := src["internal/server/proxy.go"]
	oauth := src["internal/auth/oauth.go"]

	b.WriteString("| field group | implementation source | source category |\n")
	b.WriteString("|---|---|---|\n")
	b.WriteString(fmt.Sprintf("| Claude CLI version / UA / Stainless headers / beta list | `%s` | hard-coded constants |\n", constPresence(fp, "CLICurrentVersion")))
	b.WriteString(fmt.Sprintf("| `/v1/messages` OAuth auth + Anthropic headers | `%s` | generated defaults plus client header passthrough |\n", presence(proxy, "applyAnthropicHeaders")))
	b.WriteString(fmt.Sprintf("| billing header `cc_version` / `cch` | `%s` | derived from request body with local algorithm |\n", presence(mimicry, "signBillingHeaderCCH")))
	b.WriteString(fmt.Sprintf("| `metadata.user_id.device_id` | `%s` | derived locally from account key, not read from a real machine-id store |\n", presence(mimicry, "DeviceIDFor")))
	b.WriteString(fmt.Sprintf("| bootstrap sidecars | `%s` | generated async from a hard-coded schedule |\n", presence(sidecar, "realBootstrapSteps")))
	b.WriteString(fmt.Sprintf("| GrowthBook account attributes | `%s` | mixed: OAuth file values plus hard-coded plan/tier/platform |\n", presence(sidecar, "buildGrowthBookBody")))
	b.WriteString(fmt.Sprintf("| event_logging / Datadog bodies | `%s` | low-fidelity generated heartbeat |\n", presence(sidecar, "buildHeartbeatBody")))
	b.WriteString(fmt.Sprintf("| OAuth account UUID persistence | `%s` | parsed from credential JSON when present |\n", presence(oauth, "account_uuid")))
	b.WriteString("\n")
}

func writeFindings(b *strings.Builder, rows []row, src map[string]string) {
	findings := []finding{
		{
			Area:     "Flow ordering",
			Severity: "high",
			Evidence: "Golden capture sends bootstrap/quota/telemetry before row 17 business `/v1/messages`; current `proxy.go` calls `sidecar.Notify` inside the business request path and `sidecar.go` launches goroutines.",
			Why:      "A local mock diff should verify whether first business traffic is observed before the synthetic bootstrap. If yes, the implementation is not reproducing the observed OAuth flow.",
		},
		{
			Area:     "Body shape",
			Severity: "high",
			Evidence: "Row 17 has OAuth-style `tools` count 8, `system` count 4, `diagnostics`, and `ToolSearch`; current mimicry skips full rewrite when the incoming body already has a Claude Code system prefix.",
			Why:      "A downstream API-key-shaped Claude Code request can remain API-key-shaped while receiving OAuth headers.",
		},
		{
			Area:     "Identity",
			Severity: "medium",
			Evidence: "`DeviceIDFor` derives device id from account key; row captures describe machine-id-derived device id.",
			Why:      "This is a provenance mismatch. For audit use, mark it as locally synthesized rather than authentic.",
		},
		{
			Area:     "Account attributes",
			Severity: "medium",
			Evidence: "`buildGrowthBookBody` hard-codes `subscriptionType=max` and `rateLimitTier=default_claude_max_20x`.",
			Why:      "Those fields should be treated as unverified unless sourced from a real bootstrap/profile response.",
		},
		{
			Area:     "Telemetry fidelity",
			Severity: "medium",
			Evidence: "Row 14 contains a 99-event startup batch; current heartbeat emits one generated event per tick.",
			Why:      "This is useful for detecting implementation drift, but it is not a strict reproduction of captured telemetry.",
		},
	}

	b.WriteString("## Findings\n\n")
	for _, f := range findings {
		b.WriteString(fmt.Sprintf("### %s (%s)\n\n", f.Area, f.Severity))
		b.WriteString("- Evidence: " + f.Evidence + "\n")
		b.WriteString("- Audit interpretation: " + f.Why + "\n\n")
	}
}

func writeNextSteps(b *strings.Builder) {
	b.WriteString("## Recommended Safe Next Steps\n\n")
	b.WriteString("1. Add a mock upstream transport mode that records the actual outbound requests produced by `server.forward` without contacting real upstream hosts.\n")
	b.WriteString("2. Feed representative client requests into the proxy and compare actual outbound rows against this report's golden row summaries.\n")
	b.WriteString("3. Extend this tool to ingest the mock-captured outbound rows under `audit/oauth-request-audit/captures/` and produce a three-way diff: golden capture vs implementation source vs actual outbound request.\n")
	b.WriteString("4. Keep generated reports under `audit/oauth-request-audit/reports/` so the whole audit directory can be removed cleanly.\n")
}

func findRow(rows []row, idx int) (row, bool) {
	for _, r := range rows {
		if r.Idx == idx {
			return r, true
		}
	}
	return row{}, false
}

func phaseGuess(r row) string {
	u := r.URL
	switch {
	case strings.Contains(u, "/api/eval/"):
		return "GrowthBook bootstrap"
	case strings.Contains(u, "/api/oauth/"):
		return "OAuth account bootstrap"
	case strings.Contains(u, "claude_code_grove") || strings.Contains(u, "claude_cli/bootstrap") || strings.Contains(u, "penguin"):
		return "Claude Code bootstrap"
	case strings.Contains(u, "/v1/messages") && strings.Contains(r.ReqBody, `"quota"`):
		return "quota probe"
	case strings.Contains(u, "/v1/messages"):
		return "business message"
	case strings.Contains(u, "event_logging"):
		return "event logging"
	case strings.Contains(u, "datadoghq"):
		return "Datadog logging"
	case strings.Contains(u, "mcp"):
		return "MCP discovery"
	case strings.Contains(u, "downloads.claude.ai"):
		return "release check"
	default:
		return "other"
	}
}

func bodyShape(r row) string {
	if r.BodyObject == nil {
		if strings.TrimSpace(r.ReqBody) == "" {
			return "empty"
		}
		if strings.HasPrefix(strings.TrimSpace(r.ReqBody), "[") {
			return "JSON array"
		}
		return "non-object"
	}
	var parts []string
	for _, k := range sortedKeys(r.BodyObject) {
		v := r.BodyObject[k]
		switch x := v.(type) {
		case []any:
			parts = append(parts, fmt.Sprintf("%s[%d]", k, len(x)))
		case map[string]any:
			parts = append(parts, fmt.Sprintf("%s{%d}", k, len(x)))
		default:
			parts = append(parts, k)
		}
	}
	return strings.Join(parts, ", ")
}

func headerSummary(r row) string {
	keys := []string{"authorization", "user-agent", "anthropic-beta", "anthropic-version", "x-claude-code-session-id", "x-client-request-id", "x-service-name", "accept", "accept-encoding", "connection"}
	var parts []string
	for _, k := range keys {
		if v := r.HeaderStr[k]; v != "" {
			parts = append(parts, fmt.Sprintf("`%s`=%s", k, summarize(v, 100)))
		}
	}
	return strings.Join(parts, "; ")
}

func bodySummary(r row) string {
	if r.BodyObject == nil {
		return bodyShape(r)
	}
	var parts []string
	if v, ok := r.BodyObject["model"].(string); ok {
		parts = append(parts, "`model`="+v)
	}
	if v, ok := r.BodyObject["system"].([]any); ok {
		parts = append(parts, fmt.Sprintf("`system`=%d", len(v)))
	}
	if v, ok := r.BodyObject["tools"].([]any); ok {
		parts = append(parts, fmt.Sprintf("`tools`=%d", len(v)))
	}
	if v, ok := r.BodyObject["messages"].([]any); ok {
		parts = append(parts, fmt.Sprintf("`messages`=%d", len(v)))
	}
	for _, k := range []string{"metadata", "thinking", "context_management", "output_config", "diagnostics", "stream"} {
		if _, ok := r.BodyObject[k]; ok {
			parts = append(parts, "`"+k+"`")
		}
	}
	if len(parts) == 0 {
		return bodyShape(r)
	}
	return strings.Join(parts, "; ")
}

func metadataUserID(r row) string {
	if r.BodyObject == nil {
		return ""
	}
	md, ok := r.BodyObject["metadata"].(map[string]any)
	if !ok {
		return ""
	}
	uid, ok := md["user_id"].(string)
	if !ok {
		return ""
	}
	var inner map[string]any
	if err := json.Unmarshal([]byte(uid), &inner); err != nil {
		return summarize(uid, 160)
	}
	return fmt.Sprintf("device_id=%s; account_uuid=%s; session_id=%s",
		summarize(fmt.Sprint(inner["device_id"]), 24),
		fmt.Sprint(inner["account_uuid"]),
		fmt.Sprint(inner["session_id"]))
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func trimURL(u string) string {
	u = strings.TrimPrefix(u, "https://api.anthropic.com")
	u = strings.TrimPrefix(u, "https://downloads.claude.ai")
	u = strings.TrimPrefix(u, "https://http-intake.logs.us5.datadoghq.com")
	return u
}

func summarize(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", `\n`)
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

func presence(src, needle string) string {
	if strings.Contains(src, needle) {
		return needle
	}
	return "not found"
}

func constPresence(src, needle string) string {
	if strings.Contains(src, needle) {
		return "internal/server/fingerprint.go:" + needle
	}
	return "not found"
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
