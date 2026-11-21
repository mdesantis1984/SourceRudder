// Package resultset keeps the full result list of each search in memory so a
// caller can page it without repeating the upstream search. Sets are opaque
// to callers (identified only by a random ID), bounded in count, total size
// and age, and evicted oldest-first.
package resultset

import (
	"crypto/rand"
	"encoding/base32"
	"sync"
	"time"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

const (
	// DefaultTTL is how long a stored set stays readable after creation.
	DefaultTTL = 15 * time.Minute
	// DefaultMaxSets bounds how many sets are held at once.
	DefaultMaxSets = 64
	// DefaultMaxResults bounds the results held across all sets.
	DefaultMaxResults = 2000

	idPrefix   = "rs_"
	idRawBytes = 10 // 80 bits -> exactly 16 base32 characters, no padding
	maxIDDraws = 8
)

// Config bounds the store. Non-positive values fall back to the defaults.
type Config struct {
	TTL        time.Duration
	MaxSets    int
	MaxResults int
	// Now overrides the clock; tests use it to expire sets deterministically.
	Now func() time.Time
}

// DefaultConfig returns the documented defaults: 15 minutes, 64 sets and
// 2,000 results in total.
func DefaultConfig() Config {
	return Config{TTL: DefaultTTL, MaxSets: DefaultMaxSets, MaxResults: DefaultMaxResults}
}

// Set is one stored search. Meta carries every response field except the
// results (strategy, warnings, partial flag, ...) so paging returns the same
// context as the original call.
type Set struct {
	Query string
	// DefaultLimit is the page size used when a paging call omits maxResults.
	DefaultLimit int
	Meta         types.SearchResponse
	Results      []types.SearchResultItem
}

// Stats reports current occupancy.
type Stats struct {
	Sets    int
	Results int
}

type entry struct {
	id        string
	set       Set
	createdAt time.Time
}

// Store is safe for concurrent use. It has its own lock and never calls out
// while holding it.
type Store struct {
	mu      sync.RWMutex
	cfg     Config
	entries map[string]*entry
	order   []string // insertion order, oldest first
	results int
}

// New builds a store, replacing non-positive limits with the defaults.
func New(cfg Config) *Store {
	def := DefaultConfig()
	if cfg.TTL <= 0 {
		cfg.TTL = def.TTL
	}
	if cfg.MaxSets <= 0 {
		cfg.MaxSets = def.MaxSets
	}
	if cfg.MaxResults <= 0 {
		cfg.MaxResults = def.MaxResults
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Store{cfg: cfg, entries: make(map[string]*entry)}
}

// Config returns the effective configuration.
func (s *Store) Config() Config { return s.cfg }

// Put stores a copy of set under a new opaque ID and returns the ID and how
// many results were kept. A set larger than the whole result budget keeps
// its first MaxResults results. Expired sets are purged, then the oldest
// sets are evicted until the set and result bounds hold again.
func (s *Store) Put(set Set) (string, int) {
	results := set.Results
	if len(results) > s.cfg.MaxResults {
		results = results[:s.cfg.MaxResults]
	}
	set.Results = append([]types.SearchResultItem(nil), results...)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.cfg.Now()
	s.purgeExpiredLocked(now)

	for len(s.order) > 0 && (len(s.order) >= s.cfg.MaxSets || s.results+len(set.Results) > s.cfg.MaxResults) {
		s.evictOldestLocked()
	}

	id := s.newIDLocked()
	s.entries[id] = &entry{id: id, set: set, createdAt: now}
	s.order = append(s.order, id)
	s.results += len(set.Results)
	return id, len(set.Results)
}

// Get returns a copy of the set, or false when the ID is unknown or expired.
// It never modifies the store.
func (s *Store) Get(id string) (Set, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.entries[id]
	if !ok || !s.cfg.Now().Before(e.createdAt.Add(s.cfg.TTL)) {
		return Set{}, false
	}
	out := e.set
	out.Results = append([]types.SearchResultItem(nil), e.set.Results...)
	return out, true
}

// Stats reports how many sets and results are currently held, including
// expired sets not yet purged.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{Sets: len(s.entries), Results: s.results}
}

func (s *Store) purgeExpiredLocked(now time.Time) {
	kept := s.order[:0]
	for _, id := range s.order {
		e := s.entries[id]
		if now.Before(e.createdAt.Add(s.cfg.TTL)) {
			kept = append(kept, id)
			continue
		}
		s.results -= len(e.set.Results)
		delete(s.entries, id)
	}
	s.order = kept
}

func (s *Store) evictOldestLocked() {
	id := s.order[0]
	s.order = s.order[1:]
	s.results -= len(s.entries[id].set.Results)
	delete(s.entries, id)
}

// newIDLocked draws IDs until one is unused. The ID is random and never
// derived from the query.
func (s *Store) newIDLocked() string {
	var id string
	for i := 0; i < maxIDDraws; i++ {
		raw := make([]byte, idRawBytes)
		if _, err := rand.Read(raw); err != nil {
			panic("resultset: crypto/rand failed: " + err.Error())
		}
		id = idPrefix + base32.StdEncoding.EncodeToString(raw)
		if _, taken := s.entries[id]; !taken {
			return id
		}
	}
	return id
}
