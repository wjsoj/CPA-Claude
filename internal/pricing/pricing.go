// Package pricing maps Anthropic model names to per-token USD cost and
// computes request cost from a usage.Counts payload.
//
// The cost formula is:
//
//	cost = (input*P_in + output*P_out + cacheRead*P_cr + cacheCreate*P_cw) / 1e6
//
// The built-in catalog covers the three models we use today. Extra models
// or custom overrides can be supplied via config.yaml:
//
//	pricing:
//	  default:
//	    input_per_1m: 3.00
//	    output_per_1m: 15.00
//	    cache_read_per_1m: 0.30
//	    cache_create_per_1m: 3.75
//	  models:
//	    claude-opus-4-6:
//	      input_per_1m: 5.0
//	      output_per_1m: 25.0
//	      cache_read_per_1m: 0.50
//	      cache_create_per_1m: 6.25
package pricing

import (
	"strings"

	"github.com/wjsoj/CPA-Claude/internal/usage"
)

// ModelPrice is the USD price per 1M tokens for each token class.
// Cache-read and cache-create are priced separately because Anthropic does
// charge them differently (cache_read is ~0.1× input, cache_create is ~1.25×
// input — the defaults below reflect Anthropic's published pricing).
type ModelPrice struct {
	InputPer1M       float64 `yaml:"input_per_1m" json:"input_per_1m"`
	OutputPer1M      float64 `yaml:"output_per_1m" json:"output_per_1m"`
	CacheReadPer1M   float64 `yaml:"cache_read_per_1m" json:"cache_read_per_1m"`
	CacheCreatePer1M float64 `yaml:"cache_create_per_1m" json:"cache_create_per_1m"`
}

// Cost returns USD for the given token counts under this price card.
func (p ModelPrice) Cost(c usage.Counts) float64 {
	return (float64(c.InputTokens)*p.InputPer1M +
		float64(c.OutputTokens)*p.OutputPer1M +
		float64(c.CacheReadTokens)*p.CacheReadPer1M +
		float64(c.CacheCreateTokens)*p.CacheCreatePer1M) / 1_000_000
}

// Config is the YAML shape of the `pricing` section.
type Config struct {
	Default ModelPrice            `yaml:"default"`
	Models  map[string]ModelPrice `yaml:"models"`
}

// Catalog resolves model names to a price card, falling back to the default
// when unknown.
type Catalog struct {
	defaultPrice ModelPrice
	models       map[string]ModelPrice
}

// NewCatalog merges the user config (may be zero-valued) on top of the
// built-in defaults so callers always get a sensible price for common models.
func NewCatalog(c Config) *Catalog {
	cat := &Catalog{
		defaultPrice: defaultModelPrice(),
		models:       make(map[string]ModelPrice, len(builtIn)+len(c.Models)),
	}
	for k, v := range builtIn {
		cat.models[k] = v
	}
	for k, v := range c.Models {
		cat.models[k] = v
	}
	if nonZero(c.Default) {
		cat.defaultPrice = c.Default
	}
	return cat
}

// Lookup returns the price card for a model. Matching is case-insensitive
// and also tolerates well-known prefix matches (e.g. `claude-sonnet-4-6-20260401`
// falls back to `claude-sonnet-4-6` entry).
func (c *Catalog) Lookup(model string) ModelPrice {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "" {
		return c.defaultPrice
	}
	if p, ok := c.models[m]; ok {
		return p
	}
	// Prefix fallback: try successively shorter suffix-trimmed prefixes.
	for i := strings.LastIndex(m, "-"); i > 0; i = strings.LastIndex(m[:i], "-") {
		if p, ok := c.models[m[:i]]; ok {
			return p
		}
	}
	return c.defaultPrice
}

// Cost is a convenience shortcut — Lookup(model).Cost(counts).
func (c *Catalog) Cost(model string, counts usage.Counts) float64 {
	return c.Lookup(model).Cost(counts)
}

// Models returns a copy of the registered model → price map.
func (c *Catalog) Models() map[string]ModelPrice {
	out := make(map[string]ModelPrice, len(c.models))
	for k, v := range c.models {
		out[k] = v
	}
	return out
}

// Default returns the fallback price card.
func (c *Catalog) Default() ModelPrice { return c.defaultPrice }

func nonZero(p ModelPrice) bool {
	return p.InputPer1M != 0 || p.OutputPer1M != 0 || p.CacheReadPer1M != 0 || p.CacheCreatePer1M != 0
}

func defaultModelPrice() ModelPrice {
	// Fallback when nothing else matches — matches Sonnet.
	return ModelPrice{
		InputPer1M:       3.00,
		OutputPer1M:      15.00,
		CacheReadPer1M:   0.30,
		CacheCreatePer1M: 3.75,
	}
}

// builtIn is the stock model catalog. Cache-create is derived from Anthropic's
// standard 1.25× input-price markup; cache-read follows the 0.1× input-price
// markup (matches the values the user quoted).
var builtIn = map[string]ModelPrice{
	"claude-haiku-4-5-20251001": {
		InputPer1M:       1.00,
		OutputPer1M:      5.00,
		CacheReadPer1M:   0.10,
		CacheCreatePer1M: 1.25,
	},
	"claude-haiku-4-5": {
		InputPer1M:       1.00,
		OutputPer1M:      5.00,
		CacheReadPer1M:   0.10,
		CacheCreatePer1M: 1.25,
	},
	"claude-opus-4-6": {
		InputPer1M:       5.00,
		OutputPer1M:      25.00,
		CacheReadPer1M:   0.50,
		CacheCreatePer1M: 6.25,
	},
	"claude-opus-4-7": {
		InputPer1M:       5.00,
		OutputPer1M:      25.00,
		CacheReadPer1M:   0.50,
		CacheCreatePer1M: 6.25,
	},
	"claude-sonnet-4-6": {
		InputPer1M:       3.00,
		OutputPer1M:      15.00,
		CacheReadPer1M:   0.30,
		CacheCreatePer1M: 3.75,
	},
}
