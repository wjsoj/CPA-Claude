// Package codexquota proactively refreshes the official ChatGPT Codex usage
// view so genuinely exhausted accounts leave the scheduler before a user
// request has to discover the limit.
package codexquota

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/wjsoj/cc-core/auth"
)

const probeTimeout = 30 * time.Second

// Run refreshes every enabled OpenAI OAuth credential immediately and then at
// interval until ctx is canceled. FetchCodexUsage owns the authoritative
// classification: limit_reached=true marks account quota until reset, while
// transport/probe errors do not affect credential health.
func Run(ctx context.Context, pool *auth.Pool, useUTLS bool, interval time.Duration) {
	if pool == nil {
		return
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	refresh := func() {
		for _, st := range pool.Status() {
			if st.Auth.Disabled || st.Auth.Kind != auth.KindOAuth ||
				auth.NormalizeProvider(st.Auth.Provider) != auth.ProviderOpenAI {
				continue
			}
			a := pool.FindByID(st.Auth.ID)
			if a == nil {
				continue
			}
			probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
			info, err := a.FetchCodexUsage(probeCtx, useUTLS)
			cancel()
			if err != nil {
				// The usage endpoint is auxiliary. A failed probe must never
				// degrade or freeze a credential that may still serve traffic.
				log.Warnf("codex quota probe: %s: %v", st.Auth.ID, err)
				continue
			}
			if info.RateLimit != nil && info.RateLimit.LimitReached {
				log.Infof("codex quota probe: %s reached account quota; scheduler cooldown updated", st.Auth.ID)
			}
		}
	}

	refresh()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			refresh()
		}
	}
}
