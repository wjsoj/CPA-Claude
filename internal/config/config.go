package config

import (
	"fmt"
	"os"
	"path/filepath"

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
	AccessTokens []string `yaml:"access_tokens"`

	// API-key fallback pool. No concurrency limit.
	APIKeys []APIKey `yaml:"api_keys"`

	// Default upstream proxy URL used when an OAuth file has none specified.
	DefaultProxyURL string `yaml:"default_proxy_url,omitempty"`

	// Anthropic API base URL (override for testing).
	AnthropicBaseURL string `yaml:"anthropic_base_url,omitempty"`

	// If true, OAuth/API-key refresh+request uses utls Chrome fingerprint.
	UseUTLS bool `yaml:"use_utls"`
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
}
