// Package cache provides a bounded, in-process TTL cache used by connectors
// as an ephemeral performance optimization. The cache lives in process memory
// only: there is no persistence, no exported history surface, and no MCP
// tooling that can read or invalidate it. When the process exits, the cache
// disappears with it.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"time"

	"github.com/thiscloud/ia-buscar/pkg/types"
)

type Service struct {
	entries map[string]*entry
	mu      sync.RWMutex
	ttl     time.Duration
}

type entry struct {
	key     string
	value   []byte
	created time.Time
	expires time.Time
	sources []string
}

func NewService(ttlSeconds int) *Service {
	return &Service{
		entries: make(map[string]*entry),
		ttl:     time.Duration(ttlSeconds) * time.Second,
	}
}

func (s *Service) Get(ctx context.Context, cacheKey string) (*types.CacheEntry, bool, error) {
	s.mu.RLock()
	e, ok := s.entries[cacheKey]
	s.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if time.Now().After(e.expires) {
		s.Delete(ctx, cacheKey)
		return nil, false, nil
	}
	return &types.CacheEntry{
		CacheKey:  e.key,
		CreatedAt: e.created,
		ExpiresAt: e.expires,
		Payload:   e.value,
		SourceSet: e.sources,
	}, true, nil
}

func (s *Service) Set(ctx context.Context, cacheKey string, payload []byte, sources []string) {
	now := time.Now()
	s.mu.Lock()
	s.entries[cacheKey] = &entry{
		key:     cacheKey,
		value:   payload,
		created: now,
		expires: now.Add(s.ttl),
		sources: sources,
	}
	s.mu.Unlock()
}

func (s *Service) Delete(ctx context.Context, cacheKey string) {
	s.mu.Lock()
	delete(s.entries, cacheKey)
	s.mu.Unlock()
}

// DeleteIfPresent atomically removes cacheKey and reports whether an
// entry was actually present. It is the check-and-delete primitive
// the invalidate_cache MCP tool needs so the wire response can
// distinguish "I removed your entry" from "there was nothing to
// remove" without a follow-up Get. The whole operation holds the
// write lock so concurrent DeleteIfPresent calls on the same key are
// race-safe: exactly one observer sees true.
func (s *Service) DeleteIfPresent(ctx context.Context, cacheKey string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[cacheKey]; !ok {
		return false
	}
	delete(s.entries, cacheKey)
	return true
}

func (s *Service) Clear(ctx context.Context) {
	s.mu.Lock()
	s.entries = make(map[string]*entry)
	s.mu.Unlock()
}

func (s *Service) Keys(ctx context.Context) ([]string, error) {
	s.mu.RLock()
	keys := make([]string, 0, len(s.entries))
	for k := range s.entries {
		keys = append(keys, k)
	}
	s.mu.RUnlock()
	return keys, nil
}

func GenerateCacheKey(query string, sources []string, timeRange string) string {
	h := sha256.New()
	h.Write([]byte(query))
	for _, s := range sources {
		h.Write([]byte(s))
	}
	h.Write([]byte("|tr="))
	h.Write([]byte(timeRange))
	return "cache:" + hex.EncodeToString(h.Sum(nil))[:16]
}
