package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/wjsoj/CPA-Claude/internal/pricing"
	"gopkg.in/yaml.v3"
)

type APIKey struct {
	Key      string `yaml:"key"`
	ProxyURL string `yaml:"proxy_url,omitempty"`
	Label    string `yaml:"label,omitempty"`
}

type Config struct {
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	LogLevel string `yaml:"log_level"`

	// Directory containing OAuth credential JSON files.
	AuthDir string `yaml:"auth_dir"`

	// Persistence file for usage statistics and session state.
	StateFile string `yaml:"state_file"`

	// Minutes of inactivity after which a client session releases its OAuth slot.
	ActiveWindowMinutes int `yaml:"active_window_minutes"`

	// Client-facing access tokens. Requests must present one in Authorization: Bearer.
	// Empty list disables client auth (open proxy).
	//
	// YAML accepts either form:
	//   access_tokens: ["sk-xxx", "sk-yyy"]
	//   access_tokens:
	//     - token: "sk-xxx"
	//       name: "alice"
	//     - "sk-yyy"
	AccessTokens []AccessToken `yaml:"access_tokens"`

	// Token required to access the /admin/* management panel and APIs.
	// Empty = panel disabled. Send as X-Admin-Token header (or Authorization: Bearer).
	AdminToken string `yaml:"admin_token,omitempty"`

	// API-key fallback pool. No concurrency limit.
	APIKeys []APIKey `yaml:"api_keys"`

	// Default upstream proxy URL used when an OAuth file has none specified.
	DefaultProxyURL string `yaml:"default_proxy_url,omitempty"`

	// Anthropic API base URL (override for testing).
	AnthropicBaseURL string `yaml:"anthropic_base_url,omitempty"`

	// If true, OAuth/API-key refresh+request uses utls Chrome fingerprint.
	UseUTLS bool `yaml:"use_utls"`

	// Directory for per-request JSONL logs (one file per day:
	// requests-YYYY-MM-DD.jsonl). Empty = disabled.
	LogDir string `yaml:"log_dir,omitempty"`

	// Days to retain rotated request logs. 0 = disable GC (keep forever).
	LogRetentionDays int `yaml:"log_retention_days,omitempty"`

	// Pricing overrides (optional). Built-in defaults cover claude-haiku-4-5,
	// claude-opus-4-6, and claude-sonnet-4-6.
	Pricing pricing.Config `yaml:"pricing"`

	// Per-access-token weekly USD budgets. Week boundary is ISO week (Mon
	// 00:00 UTC). Tokens not listed here are not enforced.
	ClientBudgets []ClientBudget `yaml:"client_budgets"`
}

// AccessToken is one entry in the access_tokens list. YAML can write it as
// either a bare string (only the token) or a mapping with token/name.
type AccessToken struct {
	Token string `yaml:"token"`
	Name  string `yaml:"name,omitempty"`
}

// UnmarshalYAML accepts scalar (string) or map form for backward compat.
func (a *AccessToken) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		a.Token = node.Value
		return nil
	}
	var shape struct {
		Token string `yaml:"token"`
		Name  string `yaml:"name"`
	}
	if err := node.Decode(&shape); err != nil {
		return err
	}
	a.Token = shape.Token
	a.Name = shape.Name
	return nil
}

type ClientBudget struct {
	Token     string  `yaml:"token"`
	Label     string  `yaml:"label,omitempty"`
	WeeklyUSD float64 `yaml:"weekly_usd"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	applyDefaults(cfg, path)
	return cfg, nil
}

func applyDefaults(c *Config, path string) {
	if c.Host == "" {
		c.Host = "0.0.0.0"
	}
	if c.Port == 0 {
		c.Port = 8080
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.ActiveWindowMinutes == 0 {
		c.ActiveWindowMinutes = 10
	}
	if c.AnthropicBaseURL == "" {
		c.AnthropicBaseURL = "https://api.anthropic.com"
	}
	dir := filepath.Dir(path)
	if c.AuthDir == "" {
		c.AuthDir = filepath.Join(dir, "auths")
	} else if !filepath.IsAbs(c.AuthDir) {
		c.AuthDir = filepath.Join(dir, c.AuthDir)
	}
	if c.StateFile == "" {
		c.StateFile = filepath.Join(dir, "state.json")
	} else if !filepath.IsAbs(c.StateFile) {
		c.StateFile = filepath.Join(dir, c.StateFile)
	}
	if c.LogDir != "" && !filepath.IsAbs(c.LogDir) {
		c.LogDir = filepath.Join(dir, c.LogDir)
	}
	if c.LogRetentionDays == 0 {
		c.LogRetentionDays = 30
	}
}
