// Package clienttoken is the runtime store of client access tokens that
// the proxy accepts in Authorization: Bearer.
//
// Tokens live in a single file, tokens.json, sitting next to state.json.
// The admin panel owns full CRUD.
//
// For backward compatibility, Open() accepts any legacy access_tokens
// parsed from config.yaml and any legacy "overrides" section in an older
// tokens.json, merges them into the runtime list (runtime wins on
// conflict), and reports migrated=true so the caller can scrub the
// legacy config and rewrite tokens.json without the overrides section.
package clienttoken

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wjsoj/CPA-Claude/internal/auth"
	"github.com/wjsoj/CPA-Claude/internal/config"
)

// Token is one client access token.
type Token struct {
	Token         string    `json:"token"`
	Name          string    `json:"name"`
	WeeklyUSD     float64   `json:"weekly_usd,omitempty"`
	MaxConcurrent int       `json:"max_concurrent,omitempty"` // 0 = use global default
	Group         string    `json:"group,omitempty"`          // credential group scope; empty = public
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

// View is the API representation returned to the admin panel.
type View struct {
	Token         string    `json:"token"`
	Name          string    `json:"name"`
	WeeklyUSD     float64   `json:"weekly_usd"`
	MaxConcurrent int       `json:"max_concurrent,omitempty"`
	Group         string    `json:"group,omitempty"`
	CreatedAt     time.Time `json:"created_at,omitempty"`
}

type Store struct {
	mu      sync.RWMutex
	tokens  []Token
	path    string
}

// Open loads tokens.json (if it exists) and merges any legacy tokens that
// only lived in config.yaml. Returns migrated=true when the on-disk state
// changed relative to what was read — the caller should then strip the
// legacy access_tokens block from config.yaml.
//
// path may be "" to disable persistence (tokens stay in memory only).
func Open(path string, cfgTokens []config.AccessToken) (*Store, bool, error) {
	s := &Store{path: path}

	// Legacy overrides: an older tokens.json could carry {"overrides": [...]}.
	// They applied on top of config-defined tokens. Re-apply them to the
	// migrated runtime entries, then drop.
	var legacyOverrides map[string]Token

	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			var file struct {
				Tokens    []Token `json:"tokens"`
				Overrides []Token `json:"overrides"`
			}
			if err := json.Unmarshal(data, &file); err != nil {
				return nil, false, fmt.Errorf("parse %s: %w", path, err)
			}
			for _, t := range file.Tokens {
				t.Token = strings.TrimSpace(t.Token)
				if t.Token == "" {
					continue
				}
				s.tokens = append(s.tokens, t)
			}
			if len(file.Overrides) > 0 {
				legacyOverrides = make(map[string]Token, len(file.Overrides))
				for _, t := range file.Overrides {
					t.Token = strings.TrimSpace(t.Token)
					if t.Token == "" {
						continue
					}
					legacyOverrides[t.Token] = t
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, false, err
		}
	}

	// Index existing runtime tokens for dedup against cfgTokens.
	seen := make(map[string]int, len(s.tokens))
	for i, t := range s.tokens {
		seen[t.Token] = i
	}

	migrated := false
	now := time.Now()
	for _, at := range cfgTokens {
		tok := strings.TrimSpace(at.Token)
		if tok == "" {
			continue
		}
		// Any access_tokens entry in config.yaml is a migration signal.
		migrated = true
		if _, ok := seen[tok]; ok {
			// Already in tokens.json — runtime wins, skip.
			continue
		}
		t := Token{
			Token:         tok,
			Name:          strings.TrimSpace(at.Name),
			WeeklyUSD:     at.WeeklyUSD,
			MaxConcurrent: at.MaxConcurrent,
			Group:         auth.NormalizeGroup(at.Group),
			CreatedAt:     now,
		}
		if o, ok := legacyOverrides[tok]; ok {
			t.Name = o.Name
			t.WeeklyUSD = o.WeeklyUSD
			t.MaxConcurrent = o.MaxConcurrent
			t.Group = auth.NormalizeGroup(o.Group)
		}
		s.tokens = append(s.tokens, t)
		seen[tok] = len(s.tokens) - 1
	}

	if len(legacyOverrides) > 0 {
		migrated = true
		// Apply any overrides whose target token already exists in runtime
		// (covers the edge case where a user migrated by hand but left
		// overrides behind).
		for tok, o := range legacyOverrides {
			if i, ok := seen[tok]; ok {
				if o.Name != "" {
					s.tokens[i].Name = o.Name
				}
				if o.WeeklyUSD != 0 {
					s.tokens[i].WeeklyUSD = o.WeeklyUSD
				}
				if o.MaxConcurrent != 0 {
					s.tokens[i].MaxConcurrent = o.MaxConcurrent
				}
				if g := auth.NormalizeGroup(o.Group); g != "" {
					s.tokens[i].Group = g
				}
			}
		}
	}

	if migrated {
		if err := s.saveLocked(); err != nil {
			return nil, false, fmt.Errorf("persist migrated tokens: %w", err)
		}
	}
	return s, migrated, nil
}

// Lookup reports whether tok is a known client token.
func (s *Store) Lookup(tok string) (name string, weekly float64, maxConc int, group string, ok bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, t := range s.tokens {
		if t.Token == tok {
			return t.Name, t.WeeklyUSD, t.MaxConcurrent, t.Group, true
		}
	}
	return "", 0, 0, "", false
}

// Empty reports whether the proxy should run in open mode.
func (s *Store) Empty() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tokens) == 0
}

// List returns every token as a View. Safe to serialize to the admin
// panel; do not leak to unauthenticated callers.
func (s *Store) List() []View {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]View, 0, len(s.tokens))
	for _, t := range s.tokens {
		out = append(out, View{
			Token: t.Token, Name: t.Name, WeeklyUSD: t.WeeklyUSD,
			MaxConcurrent: t.MaxConcurrent, Group: t.Group, CreatedAt: t.CreatedAt,
		})
	}
	return out
}

// Add creates a new token. Fails if one with the same value already exists.
func (s *Store) Add(t Token) error {
	t.Token = strings.TrimSpace(t.Token)
	if t.Token == "" {
		return fmt.Errorf("token required")
	}
	if t.WeeklyUSD < 0 {
		t.WeeklyUSD = 0
	}
	t.Name = strings.TrimSpace(t.Name)
	t.Group = auth.NormalizeGroup(t.Group)
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.tokens {
		if existing.Token == t.Token {
			return fmt.Errorf("token already exists")
		}
	}
	s.tokens = append(s.tokens, t)
	return s.saveLocked()
}

// Update patches an existing token. nil fields mean "no change".
func (s *Store) Update(token string, name *string, weekly *float64, maxConc *int, group *string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.tokens {
		if s.tokens[i].Token == token {
			if name != nil {
				s.tokens[i].Name = strings.TrimSpace(*name)
			}
			if weekly != nil {
				w := *weekly
				if w < 0 {
					w = 0
				}
				s.tokens[i].WeeklyUSD = w
			}
			if maxConc != nil {
				mc := *maxConc
				if mc < 0 {
					mc = 0
				}
				s.tokens[i].MaxConcurrent = mc
			}
			if group != nil {
				s.tokens[i].Group = auth.NormalizeGroup(*group)
			}
			return s.saveLocked()
		}
	}
	return fmt.Errorf("token not found")
}

// Delete removes a token.
func (s *Store) Delete(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.tokens {
		if t.Token == token {
			s.tokens = append(s.tokens[:i], s.tokens[i+1:]...)
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
	payload := struct {
		Tokens []Token `json:"tokens"`
	}{Tokens: s.tokens}
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
