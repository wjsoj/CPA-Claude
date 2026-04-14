package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/config"
	"github.com/wjsoj/CPA-Claude/internal/logging"
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

	log.Infof("loading OAuth credentials from %s", cfg.AuthDir)
	oauths, err := auth.LoadOAuthDir(cfg.AuthDir)
	if err != nil {
		log.Fatalf("load oauth dir: %v", err)
	}
	log.Infof("loaded %d OAuth credential(s)", len(oauths))
	for _, a := range oauths {
		log.Infof("  - %s (label=%s proxy=%q max_concurrent=%d)", a.ID, a.Label, a.ProxyURL, a.MaxConcurrent)
	}

	var apikeys []*auth.Auth
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
		})
	}
	log.Infof("loaded %d API key(s)", len(apikeys))

	if len(oauths) == 0 && len(apikeys) == 0 {
		log.Fatalf("no upstream credentials configured (auth_dir empty and api_keys empty)")
	}

	store, err := usage.Open(cfg.StateFile)
	if err != nil {
		log.Fatalf("open state file: %v", err)
	}

	pool := auth.NewPool(oauths, apikeys,
		time.Duration(cfg.ActiveWindowMinutes)*time.Minute,
		cfg.UseUTLS, cfg.DefaultProxyURL)

	s := server.New(cfg, pool, store)

	// Graceful shutdown. We block main on the done channel so store.Close()
	// is guaranteed to finish (final usage flush + fsync) before we exit.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		<-sigCh
		log.Info("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		store.Close()
	}()

	if err := s.Start(); err != nil {
		log.Infof("server stopped: %v", err)
	}
	<-shutdownDone
}
