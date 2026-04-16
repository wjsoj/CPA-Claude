// Package clienttoken is the runtime store of client access tokens that
// the proxy accepts in Authorization: Bearer.
//
// Two sources of truth are merged:
//
//  1. config.yaml  — the static access_tokens block. Each entry carries its
//     own token/name/weekly_usd. These stay read-only at runtime; the admin
//     panel shows them for visibility but cannot edit or delete them.
//  2. tokens.json  — a runtime file next to state.json that the admin panel
//     owns. Any CRUD from the panel writes here.
//
// The two sources are deduplicated by token string; runtime entries win.
// A single Store instance is shared by server.Server (for auth/budget
// checks on every request) and by admin.Handler (for CRUD endpoints).
package clienttoken

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/config"
)

// Token is one client access token.
type Token struct {
	Token         string    `json:"token"`
	Name          string    `json:"name"`
	WeeklyUSD     float64   `json:"weekly_usd,omitempty"`
	MaxConcurrent int       `json:"max_concurrent,omitempty"` // 0 = use global default
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

// View is the API representation returned to the admin panel. It adds
// FromConfig so the UI can lock read-only rows.
type View struct {
	Token         string    `json:"token"`
	Name          string    `json:"name"`
	WeeklyUSD     float64   `json:"weekly_usd"`
	MaxConcurrent int       `json:"max_concurrent,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
	FromConfig    bool      `json:"from_config"`
}

type Store struct {
	mu        sync.RWMutex
	cfgs      []Token          // from config.yaml, read-only base values
	runtime   []Token          // from tokens.json, mutable
	overrides map[string]Token // token -> name/weekly override applied on top of cfgs
	path      string
}

// Open loads tokens.json (if it exists) and merges config-defined tokens.
// path may be "" to disable persistence.
func Open(path string, cfgTokens []config.AccessToken) (*Store, error) {
	s := &Store{path: path, overrides: map[string]Token{}}

	for _, at := range cfgTokens {
		t := strings.TrimSpace(at.Token)
		if t == "" {
			continue
		}
		s.cfgs = append(s.cfgs, Token{
			Token:         t,
			Name:          strings.TrimSpace(at.Name),
			WeeklyUSD:     at.WeeklyUSD,
			MaxConcurrent: at.MaxConcurrent,
		})
	}

	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var file struct {
				Tokens    []Token `json:"tokens"`
				Overrides []Token `json:"overrides"`
			}
			if err := json.Unmarshal(data, &file); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
			for _, t := range file.Tokens {
				t.Token = strings.TrimSpace(t.Token)
				if t.Token == "" {
					continue
				}
				s.runtime = append(s.runtime, t)
			}
			for _, t := range file.Overrides {
				t.Token = strings.TrimSpace(t.Token)
				if t.Token == "" {
					continue
				}
				s.overrides[t.Token] = t
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return s, nil
}

// effectiveCfg returns a cfg-defined Token with any runtime override applied.
func (s *Store) effectiveCfg(t Token) Token {
	if o, ok := s.overrides[t.Token]; ok {
		t.Name = o.Name
		t.WeeklyUSD = o.WeeklyUSD
		t.MaxConcurrent = o.MaxConcurrent
	}
	return t
}

// Lookup reports whether tok is a known client token. If so, it returns the
// human-readable name, the weekly USD budget (0 = no limit), and the
// per-token max concurrent requests (0 = use global default).
func (s *Store) Lookup(tok string) (name string, weekly float64, maxConc int, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.runtime {
		if t.Token == tok {
			return t.Name, t.WeeklyUSD, t.MaxConcurrent, true
		}
	}
	for _, t := range s.cfgs {
		if t.Token == tok {
			e := s.effectiveCfg(t)
			return e.Name, e.WeeklyUSD, e.MaxConcurrent, true
		}
	}
	return "", 0, 0, false
}

// Empty reports whether the proxy should run in open mode (no tokens
// configured anywhere).
func (s *Store) Empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.cfgs) == 0 && len(s.runtime) == 0
}

// List returns config rows first, then runtime rows. Safe to serialize to
// the admin panel; do not leak to unauthenticated callers.
func (s *Store) List() []View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]View, 0, len(s.cfgs)+len(s.runtime))
	for _, t := range s.cfgs {
		e := s.effectiveCfg(t)
		out = append(out, View{
			Token: e.Token, Name: e.Name, WeeklyUSD: e.WeeklyUSD,
			MaxConcurrent: e.MaxConcurrent, CreatedAt: e.CreatedAt, FromConfig: true,
		})
	}
	for _, t := range s.runtime {
		out = append(out, View{
			Token: t.Token, Name: t.Name, WeeklyUSD: t.WeeklyUSD,
			MaxConcurrent: t.MaxConcurrent, CreatedAt: t.CreatedAt, FromConfig: false,
		})
	}
	return out
}

// Add creates a new runtime token. Fails if a token (config or runtime)
// with the same value already exists.
func (s *Store) Add(t Token) error {
	t.Token = strings.TrimSpace(t.Token)
	if t.Token == "" {
		return fmt.Errorf("token required")
	}
	if t.WeeklyUSD < 0 {
		t.WeeklyUSD = 0
	}
	t.Name = strings.TrimSpace(t.Name)
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.cfgs {
		if existing.Token == t.Token {
			return fmt.Errorf("token already defined in config.yaml")
		}
	}
	for _, existing := range s.runtime {
		if existing.Token == t.Token {
			return fmt.Errorf("token already exists")
		}
	}
	s.runtime = append(s.runtime, t)
	return s.saveLocked()
}

// Update patches an existing runtime token. nil fields mean "no change".
func (s *Store) Update(token string, name *string, weekly *float64, maxConc *int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.cfgs {
		if t.Token == token {
			cur := s.effectiveCfg(t)
			if name != nil {
				cur.Name = strings.TrimSpace(*name)
			}
			if weekly != nil {
				w := *weekly
				if w < 0 {
					w = 0
				}
				cur.WeeklyUSD = w
			}
			if maxConc != nil {
				mc := *maxConc
				if mc < 0 {
					mc = 0
				}
				cur.MaxConcurrent = mc
			}
			s.overrides[token] = Token{
				Token: token, Name: cur.Name, WeeklyUSD: cur.WeeklyUSD,
				MaxConcurrent: cur.MaxConcurrent,
			}
			return s.saveLocked()
		}
	}
	for i := range s.runtime {
		if s.runtime[i].Token == token {
			if name != nil {
				s.runtime[i].Name = strings.TrimSpace(*name)
			}
			if weekly != nil {
				w := *weekly
				if w < 0 {
					w = 0
				}
				s.runtime[i].WeeklyUSD = w
			}
			if maxConc != nil {
				mc := *maxConc
				if mc < 0 {
					mc = 0
				}
				s.runtime[i].MaxConcurrent = mc
			}
			return s.saveLocked()
		}
	}
	return fmt.Errorf("token not found")
}

// Delete removes a runtime token. Config-defined tokens cannot be deleted.
func (s *Store) Delete(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.cfgs {
		if t.Token == token {
			return fmt.Errorf("token defined in config.yaml is read-only")
		}
	}
	for i, t := range s.runtime {
		if t.Token == token {
			s.runtime = append(s.runtime[:i], s.runtime[i+1:]...)
			return s.saveLocked()
		}
	}
	return fmt.Errorf("token not found")
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	overrides := make([]Token, 0, len(s.overrides))
	for _, o := range s.overrides {
		overrides = append(overrides, o)
	}
	sort.Slice(overrides, func(i, j int) bool { return overrides[i].Token < overrides[j].Token })
	payload := struct {
		Tokens    []Token `json:"tokens"`
		Overrides []Token `json:"overrides,omitempty"`
	}{Tokens: s.runtime, Overrides: overrides}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Generate returns a fresh token in the form sk-<48 alphanumerics>.
// Matches the format in the README install snippet so users can rotate by
// hand or via the panel interchangeably.
func Generate() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	const n = 48
	max := big.NewInt(int64(len(alphabet)))
	b := make([]byte, n)
	for i := 0; i < n; i++ {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = alphabet[v.Int64()]
	}
	return "sk-" + string(b), nil
}
