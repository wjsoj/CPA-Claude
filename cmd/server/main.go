package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/codexquota"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/logging"
	"github.com/wjsoj/CPA-Claude/internal/server"
	"github.com/wjsoj/cc-core/auth"
	"github.com/wjsoj/cc-core/clienttoken"
	"github.com/wjsoj/cc-core/requestlog"
	"github.com/wjsoj/cc-core/usage"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	// Subcommand dispatch (before flag.Parse so the default server path is
	// untouched). os.Args[1] is never a flag in normal use — --config/--version
	// start with "-" — so this is unambiguous and existing invocations
	// (including the systemd ExecStart=... --config ...) keep working.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "backup":
			runBackupCmd(os.Args[2:])
			return
		case "restore":
			runRestoreCmd(os.Args[2:])
			return
		case "export-requests":
			runExportRequestsCmd(os.Args[2:])
			return
		}
	}

	configPath := flag.String("config", "config.yaml", "path to config file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("cpa-claude %s (commit=%s built=%s)\n", version, commit, buildDate)
		return
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		os.Exit(2)
	}
	logging.Setup(cfg.LogLevel)

	// Align usage/request-log day & hour buckets to the operator's local
	// time zone so the panel's "today" matches wall-clock expectations
	// instead of UTC midnight. Default Asia/Shanghai; "Local" follows the
	// host TZ. Falls back to UTC if the zone can't be loaded (no tzdata).
	if loc := cfg.DisplayLocation(); loc == nil {
		log.Warnf("display_timezone %q could not be loaded; buckets stay in UTC", cfg.DisplayTimezone)
	} else {
		usage.SetBucketLocation(loc)
		requestlog.SetBucketLocation(loc)
		log.Infof("usage/request-log buckets aligned to %s", loc)
	}

	log.Infof("loading credentials from %s", cfg.AuthDir)
	oauths, apikeysFromDir, err := auth.LoadAuthDir(cfg.AuthDir)
	if err != nil {
		log.Fatalf("load auth dir: %v", err)
	}
	log.Infof("loaded %d OAuth credential(s)", len(oauths))
	for _, a := range oauths {
		log.Infof("  - %s (label=%s proxy=%q max_concurrent=%d)", a.ID, a.Label, a.ProxyURL, a.MaxConcurrent)
	}
	log.Infof("loaded %d API-key credential(s) from auth_dir", len(apikeysFromDir))
	for _, a := range apikeysFromDir {
		log.Infof("  - %s (label=%s proxy=%q)", a.ID, a.Label, a.ProxyURL)
	}

	apikeys := apikeysFromDir
	for i, k := range cfg.APIKeys {
		if strings.TrimSpace(k.Key) == "" {
			continue
		}
		label := k.Label
		if label == "" {
			label = fmt.Sprintf("apikey-%d", i+1)
		}
		proxy := k.ProxyURL
		if proxy == "" {
			proxy = cfg.DefaultProxyURL
		}
		apikeys = append(apikeys, &auth.Auth{
			ID:          "apikey:" + label,
			Kind:        auth.KindAPIKey,
			Provider:    auth.NormalizeProvider(k.Provider),
			Label:       label,
			AccessToken: k.Key,
			ProxyURL:    proxy,
			BaseURL:     k.BaseURL,
			Group:       auth.NormalizeGroup(k.Group),
			ModelMap:    k.ModelMap,
		})
	}
	log.Infof("loaded %d API key(s)", len(apikeys))

	if len(oauths) == 0 && len(apikeys) == 0 {
		if strings.TrimSpace(cfg.AdminToken) == "" {
			log.Fatalf("no upstream credentials configured and admin panel is disabled — add credentials to auth_dir or set admin_token to bootstrap from the panel")
		}
		log.Warn("no upstream credentials loaded; waiting for admin panel uploads")
	}

	store, err := usage.Open(cfg.StateFile)
	if err != nil {
		log.Fatalf("open state file: %v", err)
	}

	var reqLog *requestlog.Writer
	var logIndex *requestlog.Store
	if cfg.LogDir != "" {
		// The index opens first because the writer may depend on it: with the
		// JSONL archive off it is the only place a record can go, and
		// OpenWithOptions refuses to start without it.
		//
		// The index is what keeps the admin panel's aggregates cheap: without
		// it every summary/pricing query re-parses the whole archive. While the
		// archive is on it is derived state, so a failure here is logged and
		// ignored — the query paths fall back to scanning JSONL on their own.
		//
		// Must come after SetBucketLocation above: the day labels the index
		// materializes are computed in the display zone.
		if !cfg.LogIndexDisabled {
			if st, err := requestlog.OpenStore(cfg.LogDir); err != nil {
				log.Warnf("request log: index unavailable (%v); queries fall back to scanning", err)
			} else {
				logIndex = st
			}
		} else {
			log.Info("request log: index disabled by config (log_index_disabled)")
		}

		reqLog, err = requestlog.OpenWithOptions(cfg.LogDir, requestlog.Options{
			RetentionDays: cfg.LogRetentionDays,
			JSONLArchive:  !cfg.LogJSONLDisabled,
		})
		if err != nil {
			log.Fatalf("open request log: %v", err)
		}
		if cfg.LogJSONLDisabled {
			log.Infof("request log: index-only at %s (retain %d days, no .jsonl archive)", cfg.LogDir, cfg.LogRetentionDays)
		} else {
			log.Infof("request log: writing to %s (retain %d days)", cfg.LogDir, cfg.LogRetentionDays)
		}
	} else {
		log.Info("request log: disabled (set log_dir in config to enable)")
	}

	pool := auth.NewPool(oauths, apikeys,
		time.Duration(cfg.ActiveWindowMinutes)*time.Minute,
		cfg.UseUTLS, cfg.DefaultProxyURL)
	pool.SetUsageLoadFunc(func(authID string) int64 {
		return store.Sum5h(authID).WeightedTotal()
	})

	// Background OAuth refresher: keeps access tokens fresh even when the
	// credential sees no traffic, so a long quiet period can't leave a token
	// expired. Single-goroutine — combined with the per-auth refresh mutex
	// this also prevents the rotating refresh_token from being burned by
	// concurrent exchanges.
	refresherCtx, refresherCancel := context.WithCancel(context.Background())
	go pool.RunRefresher(refresherCtx, time.Minute, 10*time.Minute)

	// Proactively mirror the official Codex account quota into scheduler
	// health. Model-capacity errors are intentionally request-scoped and do
	// not freeze credentials; only wham/usage limit_reached (or a genuine
	// request-time quota response) creates a quota cooldown.
	go codexquota.Run(refresherCtx, pool, cfg.UseUTLS, 5*time.Minute)

	// Daily 00:00 reset for unhealthy Anthropic API-key credentials.
	// Consecutive upstream errors auto-promote those creds to a sticky
	// hard-failure (see doForwardAnthropicAPIKey); without this job the
	// admin would have to clear them manually after every transient outage.
	go pool.RunDailyAnthropicAPIKeyReset(refresherCtx)

	tokensPath := filepath.Join(filepath.Dir(cfg.StateFile), "tokens.json")
	tokens, err := clienttoken.Open(tokensPath)
	if err != nil {
		log.Fatalf("open client token store: %v", err)
	}
	log.Infof("client tokens: %d loaded from %s", len(tokens.List()), tokensPath)

	s := server.New(cfg, pool, store, reqLog, tokens)
	for _, ep := range s.Endpoints() {
		primary := ""
		if ep.Primary {
			primary = " (primary — admin panel mounted here)"
		}
		log.Infof("endpoint %s [%s] → %s%s", ep.Name, ep.Provider, ep.Addr, primary)
	}

	// Graceful shutdown. We block main on the done channel so store.Close()
	// is guaranteed to finish (final usage flush + fsync) before we exit.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Info("shutting down...")
		refresherCancel()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		store.Close()
		// Stop the index before the writer: it tails the file the writer
		// owns, so shutting it down first means no catch-up races the final
		// flush.
		if logIndex != nil {
			logIndex.Close()
		}
		if reqLog != nil {
			reqLog.Close()
		}
	}()

	if err := s.Start(); err != nil {
		log.Infof("server stopped: %v", err)
	}
	<-shutdownDone
}
