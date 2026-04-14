// Package usage tracks per-auth token consumption and persists it to disk so
// stats survive restarts/upgrades. Updates are batched and flushed atomically.
package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type Counts struct {
	InputTokens       int64 `json:"input_tokens"`
	OutputTokens      int64 `json:"output_tokens"`
	CacheCreateTokens int64 `json:"cache_create_tokens"`
	CacheReadTokens   int64 `json:"cache_read_tokens"`
	Requests          int64 `json:"requests"`
	Errors            int64 `json:"errors"`
}

func (c *Counts) Add(o Counts) {
	c.InputTokens += o.InputTokens
	c.OutputTokens += o.OutputTokens
	c.CacheCreateTokens += o.CacheCreateTokens
	c.CacheReadTokens += o.CacheReadTokens
	c.Requests += o.Requests
	c.Errors += o.Errors
}

type PerAuth struct {
	AuthID    string    `json:"auth_id"`
	Label     string    `json:"label,omitempty"`
	Total     Counts    `json:"total"`
	LastUsed  time.Time `json:"last_used,omitempty"`
}

type State struct {
	Auths map[string]*PerAuth `json:"auths"`
}

type Store struct {
	mu       sync.Mutex
	state    *State
	path     string
	dirty    bool
	stopCh   chan struct{}
	flushInt time.Duration
}

// Open loads the state file (creating it if missing) and starts a background
// flusher. Close stops the flusher and performs one final flush.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	s := &Store{
		state:    &State{Auths: make(map[string]*PerAuth)},
		path:     path,
		stopCh:   make(chan struct{}),
		flushInt: 15 * time.Second,
	}
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			log.Warnf("usage: parse %s: %v (starting empty)", path, err)
		} else if st.Auths != nil {
			s.state = &st
		}
	}
	go s.loop()
	return s, nil
}

func (s *Store) Close() {
	close(s.stopCh)
	_ = s.Flush()
}

func (s *Store) loop() {
	t := time.NewTicker(s.flushInt)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			_ = s.Flush()
		}
	}
}

// Flush writes current state atomically if it has changed.
func (s *Store) Flush() error {
	s.mu.Lock()
	if !s.dirty {
		s.mu.Unlock()
		return nil
	}
	data, err := json.MarshalIndent(s.state, "", "  ")
	s.dirty = false
	s.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Record accumulates counts for an auth and marks dirty.
func (s *Store) Record(authID, label string, c Counts) {
	if authID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Auths[authID]
	if !ok {
		p = &PerAuth{AuthID: authID, Label: label}
		s.state.Auths[authID] = p
	}
	if label != "" {
		p.Label = label
	}
	p.Total.Add(c)
	p.LastUsed = time.Now()
	s.dirty = true
}

func (s *Store) Snapshot() map[string]PerAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]PerAuth, len(s.state.Auths))
	for k, v := range s.state.Auths {
		out[k] = *v
	}
	return out
}
