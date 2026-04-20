package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"path/filepath"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/clienttoken"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/logging"
	"github.com/wjsoj/CPA-Claude/internal/requestlog"
	"github.com/wjsoj/CPA-Claude/internal/server"
	"github.com/wjsoj/CPA-Claude/internal/usage"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
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
	if cfg.LogDir != "" {
		reqLog, err = requestlog.Open(cfg.LogDir, cfg.LogRetentionDays)
		if err != nil {
			log.Fatalf("open request log: %v", err)
		}
		log.Infof("request log: writing to %s (retain %d days)", cfg.LogDir, cfg.LogRetentionDays)
	} else {
		log.Info("request log: disabled (set log_dir in config to enable)")
	}

	pool := auth.NewPool(oauths, apikeys,
		time.Duration(cfg.ActiveWindowMinutes)*time.Minute,
		cfg.UseUTLS, cfg.DefaultProxyURL)
	pool.SetUsage24hFunc(func(authID string) int64 {
		return store.Sum24h(authID).WeightedTotal()
	})

	// Background OAuth refresher: keeps access tokens fresh even when the
	// credential sees no traffic, so a long quiet period can't leave a token
	// expired. Single-goroutine — combined with the per-auth refresh mutex
	// this also prevents the rotating refresh_token from being burned by
	// concurrent exchanges.
	refresherCtx, refresherCancel := context.WithCancel(context.Background())
	go pool.RunRefresher(refresherCtx, time.Minute, 10*time.Minute)

	tokensPath := filepath.Join(filepath.Dir(cfg.StateFile), "tokens.json")
	tokens, migrated, err := clienttoken.Open(tokensPath, cfg.AccessTokens)
	if err != nil {
		log.Fatalf("open client token store: %v", err)
	}
	if migrated {
		log.Infof("migrated legacy access_tokens from config.yaml into %s", tokensPath)
		if err := stripAccessTokensFromConfig(*configPath); err != nil {
			log.Warnf("could not rewrite %s to remove legacy access_tokens (migration still persisted in %s): %v", *configPath, tokensPath, err)
		} else {
			log.Infof("removed access_tokens block from %s (backup at %s.bak)", *configPath, *configPath)
		}
	}
	log.Infof("client tokens: %d loaded from %s", len(tokens.List()), tokensPath)

	s := server.New(cfg, pool, store, reqLog, tokens)

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
		if reqLog != nil {
			reqLog.Close()
		}
	}()

	if err := s.Start(); err != nil {
		log.Infof("server stopped: %v", err)
	}
	<-shutdownDone
}

// stripAccessTokensFromConfig rewrites configPath, removing the
// top-level access_tokens key while preserving every other key,
// comment, and the overall document layout. A .bak copy of the
// original is left next to it.
func stripAccessTokensFromConfig(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	doc := root.Content[0]
	newContent := make([]*yaml.Node, 0, len(doc.Content))
	removed := false
	for i := 0; i+1 < len(doc.Content); i += 2 {
		key := doc.Content[i]
		if key.Value == "access_tokens" {
			removed = true
			continue
		}
		newContent = append(newContent, doc.Content[i], doc.Content[i+1])
	}
	if !removed {
		return nil
	}
	doc.Content = newContent

	info, err := os.Stat(configPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(configPath+".bak", data, info.Mode().Perm()); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	tmp := configPath + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), info.Mode().Perm()); err != nil {
		return err
	}
	return os.Rename(tmp, configPath)
}
