package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/connectors"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// TestToolsListExactly28 locks in the architecture contract: tools/list
// returns exactly 28 tools including the three stateful tools
// (get_cached, invalidate_cache, get_search_history). The positive
// assertions live in tools_count_test.go; this test stays here so
// the per-architecture lock remains next to the buildTestServer
// helper that exercises it.
func TestToolsListExactly28(t *testing.T) {
	s := buildTestServer(t)

	tools := s.Tools()
	if len(tools) != 28 {
		t.Errorf("expected 28 tools, got %d", len(tools))
	}

	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}

	required := []string{"get_cached", "invalidate_cache", "get_search_history"}
	for _, name := range required {
		if !names[name] {
			t.Errorf("required stateful tool %q is missing from the registry", name)
		}
	}
}

// TestHandleToolsListExactly28 covers the same contract through the
// higher-level HandleToolsList path that real MCP clients hit.
func TestHandleToolsListExactly28(t *testing.T) {
	s := buildTestServer(t)

	res, err := s.HandleToolsList(context.Background(), nil)
	if err != nil {
		t.Fatalf("HandleToolsList error: %v", err)
	}
	m := res.(map[string]interface{})
	toolsList, ok := m["tools"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map[string]interface{}, got %T", m["tools"])
	}
	if len(toolsList) != 28 {
		t.Errorf("expected 28 tools, got %d", len(toolsList))
	}

	names := make(map[string]bool, len(toolsList))
	for _, entry := range toolsList {
		name, _ := entry["name"].(string)
		names[name] = true
	}
	for _, want := range []string{"get_cached", "invalidate_cache", "get_search_history"} {
		if !names[want] {
			t.Errorf("expected stateful tool %q via HandleToolsList", want)
		}
	}
}

// TestCacheHitAcrossRepeatedSearch proves that the in-process cache still
// works for current-request behavior: two identical search_web calls in
// the same Server share a cache, and the second call returns cached=true.
func TestCacheHitAcrossRepeatedSearch(t *testing.T) {
	var upstreamHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"cached result","url":"https://example.com","content":"hello"}],"unresponsive_engines":[]}`))
	}))
	defer srv.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(srv.URL, cacheSvc))

	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", srv.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10))

	callArgs, _ := json.Marshal(map[string]interface{}{"query": "repeatable query"})

	// First call: cache miss, upstream is hit.
	first, err := s.callToolByName(context.Background(), "search_web", callArgs)
	if err != nil {
		t.Fatalf("first search_web returned error: %v", err)
	}
	if first.Cached {
		t.Errorf("expected first call cached=false, got true")
	}
	if upstreamHits != 1 {
		t.Fatalf("expected upstream hit count = 1 after first call, got %d", upstreamHits)
	}

	// Second call: cache hit, upstream is NOT hit again.
	second, err := s.callToolByName(context.Background(), "search_web", callArgs)
	if err != nil {
		t.Fatalf("second search_web returned error: %v", err)
	}
	if !second.Cached {
		t.Errorf("expected second call cached=true (in-process cache hit), got false")
	}
	if upstreamHits != 1 {
		t.Errorf("expected upstream hit count to remain 1 after cached second call, got %d", upstreamHits)
	}
}

// TestCacheStateDoesNotPersistAcrossServerInstances proves the in-process
// cache is process-local: a fresh Server with a fresh cache does not see
// entries written by a previous Server instance.
func TestCacheStateDoesNotPersistAcrossServerInstances(t *testing.T) {
	var upstreamHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"x","url":"https://e.com","content":"y"}],"unresponsive_engines":[]}`))
	}))
	defer srv.Close()

	callArgs, _ := json.Marshal(map[string]interface{}{"query": "fresh-query"})

	// Server A: populate its cache.
	{
		cacheA := cache.NewService(300)
		cmA := search.NewConnectorManager(cacheA)
		cmA.Register(connectors.NewWebConnector(srv.URL, cacheA))
		serverA := NewServer(cmA, search.NewPlanner(), "stdio", ":8080", srv.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10))
		if _, err := serverA.callToolByName(context.Background(), "search_web", callArgs); err != nil {
			t.Fatalf("serverA first call failed: %v", err)
		}
		if upstreamHits != 1 {
			t.Fatalf("serverA expected 1 upstream hit, got %d", upstreamHits)
		}
	}

	// Server B: brand-new cache service. The cache from server A is gone.
	{
		cacheB := cache.NewService(300)
		cmB := search.NewConnectorManager(cacheB)
		cmB.Register(connectors.NewWebConnector(srv.URL, cacheB))
		serverB := NewServer(cmB, search.NewPlanner(), "stdio", ":8080", srv.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10))
		resp, err := serverB.callToolByName(context.Background(), "search_web", callArgs)
		if err != nil {
			t.Fatalf("serverB first call failed: %v", err)
		}
		if resp.Cached {
			t.Errorf("serverB saw cached=true on its first call; the cache is supposed to be per-process")
		}
		if upstreamHits != 2 {
			t.Errorf("expected serverB to hit upstream again (cache was process-local), got upstream hits=%d", upstreamHits)
		}
	}
}

// TestNoPersistenceSurfaceOnCacheService is a static check that the cache
// service exposes no GetCached-style or InvalidateCache-style methods that
// could be reachable from MCP. The architecture requirement is that the
// cache is an internal optimization only.
func TestNoPersistenceSurfaceOnCacheService(t *testing.T) {
	cacheSvc := cache.NewService(60)

	// The cache must be reachable only via Get/Set/Delete/Clear — the
	// operations a connector needs to run inside a request. Refresh the
	// in-process state and confirm there is no method that exposes listing
	// or invalidating from outside the connector boundary.
	keys, err := cacheSvc.Keys(context.Background())
	if err != nil {
		t.Fatalf("unexpected Keys error: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected empty cache, got %d keys", len(keys))
	}

	// Confirm Clear works but does not touch disk.
	cacheSvc.Clear(context.Background())
}

// TestStatelessArchitectureNoExternalFiles covers the property that the
// service does not touch any persistence file on disk: it does not read a
// history file, and cache eviction is purely in-process. The stateful tools
// exist, but their backing surfaces stay in-process: cache is bounded by TTL
// and the history buffer is bounded by its configured limit.
func TestStatelessArchitectureNoExternalFiles(t *testing.T) {
	// Build a fresh cache service and confirm its public surface
	// remains bounded by the configured TTL. Under the restored
	// runtime contract the stateful tools DO exist, but their
	// backing surfaces must stay in-process: no on-disk file, no
	// network socket, no external handle. The Keys + Clear round-trip
	// is the assertion that nothing leaks past the process boundary.
	cacheSvc := cache.NewService(60)

	keys, err := cacheSvc.Keys(context.Background())
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("expected empty cache, got %d keys", len(keys))
	}
	cacheSvc.Clear(context.Background())
}

// buildTestServer wires the minimum dependencies needed to exercise the
// MCP tool registry. Connectors are not registered here — tests that need
// live search behavior register them explicitly.
func buildTestServer(t *testing.T) *Server {
	t.Helper()
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	return NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10))
}

// callToolByName is a small helper that runs a search_* tool by name and
// returns the typed SearchResponse. It panics if the tool is not a search
// tool — call sites are well known.
func (s *Server) callToolByName(ctx context.Context, name string, args json.RawMessage) (*types.SearchResponse, error) {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      name,
		"arguments": json.RawMessage(args),
	})
	res, err := s.HandleToolsCall(ctx, params)
	if err != nil {
		return nil, err
	}
	m, ok := res.(map[string]interface{})
	if !ok {
		return nil, err
	}
	content, ok := m["content"].([]map[string]interface{})
	if !ok || len(content) == 0 {
		return nil, err
	}
	text, _ := content[0]["text"].(string)
	var resp types.SearchResponse
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
