// Package usage tracks per-auth token consumption and persists it to disk
// so stats survive restarts/upgrades. Updates are batched (5s ticker) and
// flushed atomically with fsync. Daily buckets give per-day totals.
package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

// How many days of per-day history to keep. Older buckets are trimmed on
// each Record() so state.json stays bounded.
const dailyRetentionDays = 90

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

// DayEntry pairs a YYYY-MM-DD date with its counters for JSON rendering.
type DayEntry struct {
	Date   string `json:"date"`
	Counts Counts `json:"counts"`
}

type PerAuth struct {
	AuthID   string            `json:"auth_id"`
	Label    string            `json:"label,omitempty"`
	Total    Counts            `json:"total"`
	LastUsed time.Time         `json:"last_used,omitempty"`
	Daily    map[string]Counts `json:"daily,omitempty"` // key = "YYYY-MM-DD" (UTC)
}

// DailyOrdered returns the Daily map as a slice sorted by date ascending.
func (p *PerAuth) DailyOrdered(maxDays int) []DayEntry {
	if len(p.Daily) == 0 {
		return nil
	}
	keys := make([]string, 0, len(p.Daily))
	for k := range p.Daily {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if maxDays > 0 && len(keys) > maxDays {
		keys = keys[len(keys)-maxDays:]
	}
	out := make([]DayEntry, 0, len(keys))
	for _, k := range keys {
		out = append(out, DayEntry{Date: k, Counts: p.Daily[k]})
	}
	return out
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
	doneCh   chan struct{}
	flushInt time.Duration
	now      func() time.Time // injectable clock (for tests)
}

// Open loads the state file (creating it if missing) and starts a background
// flusher. Close stops the flusher and performs one final fsynced flush.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	s := &Store{
		state:    &State{Auths: make(map[string]*PerAuth)},
		path:     path,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		flushInt: 5 * time.Second,
		now:      time.Now,
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
	select {
	case <-s.stopCh:
		// already closed
	default:
		close(s.stopCh)
	}
	<-s.doneCh
	_ = s.Flush()
}

func (s *Store) loop() {
	defer close(s.doneCh)
	t := time.NewTicker(s.flushInt)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			if err := s.Flush(); err != nil {
				log.Warnf("usage: periodic flush: %v", err)
			}
		}
	}
}

// Flush writes state atomically (tmp + rename + fsync) if dirty.
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
	return writeAtomic(s.path, data)
}

// writeAtomic writes data via a tmp file + rename, then fsyncs the renamed
// file so it's durable across power loss (best-effort; filesystem dependent).
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// fsync the final file to flush metadata too.
	if final, err := os.OpenFile(path, os.O_RDONLY, 0); err == nil {
		_ = final.Sync()
		_ = final.Close()
	}
	return nil
}

// Record accumulates counts for an auth (both lifetime total and today's
// daily bucket) and marks dirty.
func (s *Store) Record(authID, label string, c Counts) {
	if authID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Auths[authID]
	if !ok {
		p = &PerAuth{AuthID: authID, Label: label, Daily: make(map[string]Counts)}
		s.state.Auths[authID] = p
	}
	if p.Daily == nil {
		p.Daily = make(map[string]Counts)
	}
	if label != "" {
		p.Label = label
	}
	now := s.now()
	p.Total.Add(c)
	p.LastUsed = now
	day := now.UTC().Format("2006-01-02")
	cur := p.Daily[day]
	cur.Add(c)
	p.Daily[day] = cur
	s.trimDailyLocked(p, now)
	s.dirty = true
}

func (s *Store) trimDailyLocked(p *PerAuth, now time.Time) {
	if len(p.Daily) <= dailyRetentionDays {
		return
	}
	cutoff := now.UTC().AddDate(0, 0, -dailyRetentionDays).Format("2006-01-02")
	for k := range p.Daily {
		if k < cutoff {
			delete(p.Daily, k)
		}
	}
}

// Snapshot returns a deep copy of current per-auth counts. Safe for JSON
// rendering by the admin handler.
func (s *Store) Snapshot() map[string]PerAuth {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]PerAuth, len(s.state.Auths))
	for k, v := range s.state.Auths {
		cp := *v
		if v.Daily != nil {
			cp.Daily = make(map[string]Counts, len(v.Daily))
			for dk, dv := range v.Daily {
				cp.Daily[dk] = dv
			}
		}
		out[k] = cp
	}
	return out
}

// Sum24h returns the total counts over the last 24 hours (UTC), using the
// last two daily buckets. This is approximate — it sums today + yesterday's
// buckets rather than a strict rolling window. Good enough for dashboards.
func (s *Store) Sum24h(authID string) Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.state.Auths[authID]
	if !ok {
		return Counts{}
	}
	now := s.now().UTC()
	today := now.Format("2006-01-02")
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	var sum Counts
	sum.Add(p.Daily[today])
	sum.Add(p.Daily[yesterday])
	return sum
}
