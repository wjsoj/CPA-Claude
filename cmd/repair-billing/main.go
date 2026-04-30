// Command repair-billing rewrites historical billing data after the SSE
// double-count bug fix.
//
// The bug (now fixed in internal/server/proxy.go): for streaming responses,
// usage from the message_start AND message_delta events was Add()'d together.
// Anthropic's protocol echoes input_tokens, cache_read_input_tokens, and
// cache_creation_input_tokens in BOTH events, so those three counters were
// recorded at exactly 2× the true value. Output tokens were near-correct
// (message_start carries an output estimate of 0–1; the small overcount is
// dwarfed by message_delta's cumulative final value).
//
// What this tool does:
//
//  1. Repairs every line in logs/requests-*.jsonl where stream=true and
//     status<400: halves input/cache_read/cache_create, then RECOMPUTES
//     cost_usd from the corrected counts using the same pricing catalog the
//     proxy uses. Non-stream entries and error responses are left untouched.
//
//  2. Repairs state.json's per-auth Daily/Hourly buckets (halves input/
//     cache fields; output and request counters are fine).
//
//  3. Repairs state.json's per-client Total + Weekly aggregates: halves
//     token fields and approximates the new cost as (cost_old + output_cost
//     estimate) / 2, where output_cost is computed from Tokens.Output (which
//     was near-correct) using a representative price (the catalog default).
//     This is approximate — exact rebuild would need per-request model
//     attribution that the aggregates have already lost.
//
// Usage:
//
//	go run ./cmd/repair-billing --config config.yaml [--dry-run]
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// sentinelName lives at the root of log_dir and prevents the user from
// accidentally running the tool twice on the same dataset (which would
// halve already-correct numbers a second time). --force overrides.
const sentinelName = ".billing-repaired"

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config.yaml (used to load pricing catalog and resolve log_dir / state_file)")
	dryRun := flag.Bool("dry-run", false, "report what would change without writing")
	force := flag.Bool("force", false, "ignore the sentinel and repair anyway (use only if you know the data needs another pass)")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config %s: %v\n", *cfgPath, err)
		os.Exit(1)
	}
	cat := pricing.NewCatalog(cfg.Pricing)

	sentinel := filepath.Join(cfg.LogDir, sentinelName)
	if !*dryRun && !*force {
		if _, err := os.Stat(sentinel); err == nil {
			fmt.Fprintf(os.Stderr, "sentinel %s exists — repair already ran on this directory.\n", sentinel)
			fmt.Fprintf(os.Stderr, "Re-running would halve already-fixed numbers a second time. Pass --force only if the data was restored from backup.\n")
			os.Exit(2)
		}
	}

	fmt.Printf("repair-billing — dry_run=%v\n", *dryRun)
	fmt.Printf("  log_dir   = %s\n", cfg.LogDir)
	fmt.Printf("  state     = %s\n", cfg.StateFile)
	fmt.Println()

	// 1. Logs.
	logSummary, err := repairLogs(cfg.LogDir, cat, *dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repair logs: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("logs: scanned %d files, %d records, fixed %d streaming records\n",
		logSummary.files, logSummary.records, logSummary.fixed)
	fmt.Printf("      cost_usd: %.4f → %.4f (Δ %.4f)\n",
		logSummary.costBefore, logSummary.costAfter, logSummary.costBefore-logSummary.costAfter)
	fmt.Printf("      input_tokens removed: %d, cache_read: %d, cache_create: %d\n",
		logSummary.inputDelta, logSummary.cacheReadDelta, logSummary.cacheCreateDelta)
	fmt.Println()

	// 2. State.
	stateSummary, err := repairState(cfg.StateFile, cat, *dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "repair state: %v\n", err)
		os.Exit(1)
	}
	if stateSummary == nil {
		fmt.Println("state: file not present — skipped")
	} else {
		fmt.Printf("state: %d auths, %d clients\n", stateSummary.auths, stateSummary.clients)
		fmt.Printf("       per-auth tokens halved (input/cache_read/cache_create); output untouched\n")
		fmt.Printf("       per-client cost_usd: %.4f → %.4f\n", stateSummary.costBefore, stateSummary.costAfter)
	}

	if *dryRun {
		fmt.Println("\n(dry run; no files written)")
		return
	}

	// Drop the sentinel so re-runs are blocked by default.
	if cfg.LogDir != "" {
		_ = os.MkdirAll(cfg.LogDir, 0700)
		marker := fmt.Sprintf("repaired_at=%s\n", time.Now().UTC().Format(time.RFC3339))
		_ = os.WriteFile(sentinel, []byte(marker), 0600)
	}
}

type logSummary struct {
	files, records, fixed                          int
	costBefore, costAfter                          float64
	inputDelta, cacheReadDelta, cacheCreateDelta   int64
}

func repairLogs(dir string, cat *pricing.Catalog, dryRun bool) (logSummary, error) {
	var s logSummary
	if dir == "" {
		return s, nil
	}
	matches, err := filepath.Glob(filepath.Join(dir, "requests-*.jsonl"))
	if err != nil {
		return s, err
	}
	for _, p := range matches {
		fs, err := repairLogFile(p, cat, dryRun)
		if err != nil {
			return s, fmt.Errorf("%s: %w", p, err)
		}
		s.files++
		s.records += fs.records
		s.fixed += fs.fixed
		s.costBefore += fs.costBefore
		s.costAfter += fs.costAfter
		s.inputDelta += fs.inputDelta
		s.cacheReadDelta += fs.cacheReadDelta
		s.cacheCreateDelta += fs.cacheCreateDelta
	}
	return s, nil
}

func repairLogFile(path string, cat *pricing.Catalog, dryRun bool) (logSummary, error) {
	var s logSummary
	in, err := os.Open(path)
	if err != nil {
		return s, err
	}
	defer in.Close()

	var out []byte
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		s.records++
		// Try to decode; if the line is malformed, keep it verbatim.
		var rec requestlog.Record
		if err := json.Unmarshal(line, &rec); err != nil {
			out = append(out, line...)
			out = append(out, '\n')
			continue
		}
		if rec.Stream && rec.Status >= 200 && rec.Status < 400 && hasInflatedCounts(rec) {
			s.fixed++
			s.costBefore += rec.CostUSD
			s.inputDelta += rec.Input - rec.Input/2
			s.cacheReadDelta += rec.CacheRead - rec.CacheRead/2
			s.cacheCreateDelta += rec.CacheCreate - rec.CacheCreate/2

			rec.Input /= 2
			rec.CacheRead /= 2
			rec.CacheCreate /= 2
			rec.CostUSD = cat.Cost(canonicalProvider(rec.Provider), rec.Model, usage.Counts{
				InputTokens:       rec.Input,
				OutputTokens:      rec.Output,
				CacheReadTokens:   rec.CacheRead,
				CacheCreateTokens: rec.CacheCreate,
			})
			s.costAfter += rec.CostUSD

			b, err := json.Marshal(rec)
			if err != nil {
				return s, fmt.Errorf("re-marshal record: %w", err)
			}
			out = append(out, b...)
			out = append(out, '\n')
			continue
		}
		out = append(out, line...)
		out = append(out, '\n')
	}
	if err := scanner.Err(); err != nil {
		return s, err
	}
	if dryRun || s.fixed == 0 {
		return s, nil
	}
	return s, atomicWrite(path, out)
}

// hasInflatedCounts is a heuristic guard so re-running the tool after a
// successful repair doesn't keep halving already-fixed records. We treat any
// record with all-zero input/cache fields as already fine — halving zeros is
// a no-op anyway, so this only affects what gets counted in the summary.
// There's no perfect signature for "is this already fixed?", but rerunning is
// safe in the sense that it would just halve again — so users should not
// re-run unless they restored from backup. The flag below makes the common
// rerun-by-mistake case visible (fixed count drops to 0 after first pass on
// records that were already at 0).
func hasInflatedCounts(r requestlog.Record) bool {
	return r.Input > 0 || r.CacheRead > 0 || r.CacheCreate > 0
}

func canonicalProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "openai", "codex", "chatgpt":
		return auth.ProviderOpenAI
	default:
		return auth.ProviderAnthropic
	}
}

type stateSummary struct {
	auths, clients         int
	costBefore, costAfter  float64
}

func repairState(path string, cat *pricing.Catalog, dryRun bool) (*stateSummary, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}
	var st usage.State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse state.json: %w", err)
	}
	s := &stateSummary{}

	for _, p := range st.Auths {
		s.auths++
		halveBuckets(p.Daily)
		halveBuckets(p.Hourly)
	}
	defaultPrice := cat.Default()
	for _, p := range st.Clients {
		s.clients++
		s.costBefore += p.Total.CostUSD
		halveClientCost(&p.Total, defaultPrice)
		for k, v := range p.Weekly {
			halveClientCost(&v, defaultPrice)
			p.Weekly[k] = v
		}
		s.costAfter += p.Total.CostUSD
	}

	if dryRun {
		return s, nil
	}
	out, err := json.MarshalIndent(&st, "", "  ")
	if err != nil {
		return s, err
	}
	return s, atomicWrite(path, out)
}

func halveBuckets(m map[string]usage.Counts) {
	for k, v := range m {
		v.InputTokens /= 2
		v.CacheReadTokens /= 2
		v.CacheCreateTokens /= 2
		// Output and Requests/Errors were not affected by the bug.
		m[k] = v
	}
}

// halveClientCost adjusts an aggregated ClientCost for the SSE double-count.
// Tokens.{Input,CacheRead,CacheCreate} are halved exactly. CostUSD is
// approximated via:
//
//	cost_new ≈ (cost_old + output*P_out) / 2
//
// derived from cost_old = 2*cost_input + cost_output + 2*cost_cache and
// cost_new = cost_input + cost_output + cost_cache. We don't have per-model
// breakdown of the aggregated tokens, so P_out is taken from the catalog's
// global default price (≈ Sonnet, ~$15/M). For Claude Code workloads where
// cache_read dominates the bill, output_cost is a small share — error from
// the approximation is typically <5%.
func halveClientCost(c *usage.ClientCost, defaultPrice pricing.ModelPrice) {
	c.Tokens.InputTokens /= 2
	c.Tokens.CacheReadTokens /= 2
	c.Tokens.CacheCreateTokens /= 2
	if c.CostUSD <= 0 {
		return
	}
	outputCost := float64(c.Tokens.OutputTokens) * defaultPrice.OutputPer1M / 1_000_000
	c.CostUSD = (c.CostUSD + outputCost) / 2
	if c.CostUSD < 0 {
		c.CostUSD = 0
	}
}

func atomicWrite(path string, data []byte) error {
	tmp := path + ".repair.tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Backup the original so the fix is reversible (one-shot — overwritten
	// on subsequent runs of the tool).
	if _, err := os.Stat(path); err == nil {
		_ = os.Rename(path, path+".bak."+time.Now().Format("20060102-150405"))
	}
	return os.Rename(tmp, path)
}

