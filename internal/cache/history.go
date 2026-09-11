package cache

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Entry is one record of a search invocation that the HistoryService
// retains for the get_search_history tool. Query is the user-visible
// search string; Source is the connector name that answered the
// call; Timestamp is when the call was appended.
type Entry struct {
	Query     string    `json:"query"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
}

// HistoryService is an in-process, bounded, thread-safe ring buffer of
// recent search invocations. It backs the get_search_history MCP tool
// so AI agents can audit what the operator searched through this
// service in the current process. The buffer is intentionally small
// (default bound: 100 entries) and disappears when the process exits;
// it never reaches disk or an external service.
type HistoryService struct {
	mu      sync.Mutex
	entries []Entry
	bound   int
}

// NewHistoryService constructs a HistoryService with the given bound.
// A bound <= 0 falls back to 100 so callers that pass zero on
// accident still get a working history. The bound is the upper limit
// on the in-memory entries; List can request fewer.
func NewHistoryService(bound int) *HistoryService {
	if bound <= 0 {
		bound = 100
	}
	return &HistoryService{
		entries: make([]Entry, 0, bound),
		bound:   bound,
	}
}

// Append records one entry to the buffer. When the buffer is at its
// bound, the oldest entry is dropped to keep the bound invariant. The
// timestamp is stamped here so every entry carries the moment it was
// appended (not the moment the connector finished).
func (s *HistoryService) Append(_ context.Context, e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	if len(s.entries) >= s.bound {
		// Drop the oldest entry. Newest-first order means the oldest
		// sits at the tail, so we trim there before prepending.
		s.entries = s.entries[:len(s.entries)-1]
	}
	// Prepend so the newest entry sits at index 0; List can then
	// return prefixes without any extra sorting.
	s.entries = append([]Entry{e}, s.entries...)
	return nil
}

// List returns up to `limit` entries newest-first, optionally
// narrowed to entries whose stored Query contains `query` as a
// case-sensitive substring. A zero limit means "use the service
// bound"; an empty query returns the full bounded buffer. The result
// is a fresh slice so callers can mutate it freely.
func (s *HistoryService) List(_ context.Context, limit int, query string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	src := s.entries
	if query != "" {
		filtered := make([]Entry, 0, len(src))
		for _, e := range src {
			if strings.Contains(e.Query, query) {
				filtered = append(filtered, e)
			}
		}
		src = filtered
	}
	if limit <= 0 || limit > len(src) {
		limit = len(src)
	}
	out := make([]Entry, limit)
	copy(out, src[:limit])
	return out
}

// Len returns the current number of entries in the buffer. Exposed for
// tests so they can verify the bound invariant without exporting the
// internal slice.
func (s *HistoryService) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
