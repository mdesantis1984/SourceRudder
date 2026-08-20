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
	key      string
	value    []byte
	created  time.Time
	expires  time.Time
	sources  []string
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

func (s *Service) Set(ctx context.Context, cacheKey string, payload []byte, sources []string) error {
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
	return nil
}

func (s *Service) Delete(ctx context.Context, cacheKey string) error {
	s.mu.Lock()
	delete(s.entries, cacheKey)
	s.mu.Unlock()
	return nil
}

func (s *Service) Clear(ctx context.Context) error {
	s.mu.Lock()
	s.entries = make(map[string]*entry)
	s.mu.Unlock()
	return nil
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

func GenerateCacheKey(query string, sources []string) string {
	h := sha256.New()
	h.Write([]byte(query))
	for _, s := range sources {
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
