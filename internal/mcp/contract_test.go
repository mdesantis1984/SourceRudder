package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/connectors"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/memory"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

// TestStableEmptyArrayContract locks the wire contract from item 1: every
// MCP search response must serialize `results` as a JSON array (never
// null, never omitted), even when the upstream returns zero results and
// even when the cached payload predates the contract fix and lacks the
// field entirely. The test asserts through the real MCP boundary
// (HandleToolsCall) on both fresh and cache-hit paths.
func TestStableEmptyArrayContract(t *testing.T) {
	// Fresh path: healthy 200 OK with empty results.
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(searxng.URL, cacheSvc))
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", searxng.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10), memory.NewClient("", ""))

	args, _ := json.Marshal(map[string]interface{}{"query": "stable-empty-fresh"})
	resp, err := s.callToolByName(context.Background(), "search_web", args)
	if err != nil {
		t.Fatalf("fresh call: unexpected error: %v", err)
	}
	if resp.Results == nil {
		t.Fatal("fresh path: Results must be a non-nil empty slice, got nil")
	}
	if len(resp.Results) != 0 {
		t.Errorf("fresh path: expected 0 results, got %d", len(resp.Results))
	}

	// Now serialize the response the way the MCP transport does and
	// inspect the raw JSON to prove `results` survives as `[]`, not
	// null and not omitted.
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"results":[]`) {
		t.Errorf("fresh path: expected `\"results\":[]` in JSON, got %s", raw)
	}
	if strings.Contains(string(raw), `"results":null`) {
		t.Errorf("fresh path: must never serialize results as null; got %s", raw)
	}

	// Cache-hit path: seed the cache with a payload that predates the
	// contract fix (no `results` field at all), then issue the same
	// query again. The response must still surface `results: []`.
	cacheSvc.Set(context.Background(), cache.GenerateCacheKey("stable-empty-fresh", []string{"searxng"}, ""), []byte(`{"query":"stable-empty-fresh","partial":false}`), []string{"searxng"})

	resp2, err := s.callToolByName(context.Background(), "search_web", args)
	if err != nil {
		t.Fatalf("cache-hit call: unexpected error: %v", err)
	}
	if !resp2.Cached {
		t.Errorf("cache-hit path: expected Cached=true, got false")
	}
	if resp2.Results == nil {
		t.Fatal("cache-hit path: Results must be a non-nil empty slice, got nil")
	}
	if len(resp2.Results) != 0 {
		t.Errorf("cache-hit path: expected 0 results, got %d", len(resp2.Results))
	}

	raw2, err := json.Marshal(resp2)
	if err != nil {
		t.Fatalf("marshal cache-hit: %v", err)
	}
	if !strings.Contains(string(raw2), `"results":[]`) {
		t.Errorf("cache-hit path: expected `\"results\":[]` in JSON, got %s", raw2)
	}
	if strings.Contains(string(raw2), `"results":null`) {
		t.Errorf("cache-hit path: must never serialize results as null; got %s", raw2)
	}
}

// TestSearchDocOficialStrategySignal drives the public MCP boundary for a
// registered library. It must report an authoritative registry-backed result,
// rather than the generic web fallback that issue #20 fixes.
func TestSearchDocOficialStrategySignal(t *testing.T) {
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[{"title":"Go documentation","url":"https://go.dev/doc/","content":"hello"}],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	webConnector := connectors.NewWebConnector(searxng.URL, cacheSvc)
	cm.Register(webConnector)
	cm.Register(connectors.NewOfficialDocsConnector(webConnector))
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", searxng.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10), memory.NewClient("", ""))

	resp, err := s.callToolByName(context.Background(), "search_doc_oficial", []byte(`{"query":"go documentation"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Strategy != "official_doc_registry_search" {
		t.Errorf("expected Strategy=official_doc_registry_search, got %q", resp.Strategy)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected non-empty authoritative result")
	}
	if resp.Results[0].URL != "https://go.dev/doc/" {
		t.Errorf("expected validated Go documentation URL, got %q", resp.Results[0].URL)
	}
	if !containsString(resp.Results[0].Tags, "official-documentation") {
		t.Errorf("expected official-documentation provenance tag, got %v", resp.Results[0].Tags)
	}
	if !containsString(resp.SourcesUsed, "official_docs") {
		t.Errorf("expected official_docs source metadata, got %v", resp.SourcesUsed)
	}

	cachedResp, err := s.callToolByName(context.Background(), "search_doc_oficial", []byte(`{"query":"go documentation"}`))
	if err != nil {
		t.Fatalf("cached call: unexpected error: %v", err)
	}
	if !cachedResp.Cached || !containsString(cachedResp.SourcesUsed, "official_docs") {
		t.Errorf("expected cached official response metadata, got cached=%v sources=%v", cachedResp.Cached, cachedResp.SourcesUsed)
	}

	// Tool description must document both the registry path and fallback.
	for _, tool := range s.Tools() {
		if tool.Name == "search_doc_oficial" {
			description := strings.ToLower(tool.Description)
			if !strings.Contains(description, "official_doc_registry_search") || !strings.Contains(description, "official_doc_web_fallback") {
				t.Errorf("tool description should advertise registry and fallback strategies, got %q", tool.Description)
			}
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// TestSearchLocalIndexUnavailableSignal proves the disabled-by-default path
// never silently routes to generic web search.
func TestSearchLocalIndexUnavailableSignal(t *testing.T) {
	// Stand up a web connector that should NOT be hit. If the handler
	// routed to web, this counter would tick.
	var webHits int
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		webHits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(searxng.URL, cacheSvc))
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", searxng.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10), memory.NewClient("", ""))

	resp, err := s.callToolByName(context.Background(), "search_local_index", []byte(`{"query":"find in repo"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Strategy != "local_index_unavailable" {
		t.Errorf("expected Strategy=local_index_unavailable, got %q", resp.Strategy)
	}
	if resp.Results == nil {
		t.Fatal("expected non-nil Results, got nil")
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected empty Results, got %d items", len(resp.Results))
	}
	if webHits != 0 {
		t.Errorf("expected zero hits on the web fallback, got %d (search_local_index must NOT route to web)", webHits)
	}
	foundWarning := false
	for _, w := range resp.Warnings {
		if strings.Contains(w, "local_index_unavailable") {
			foundWarning = true
			break
		}
	}
	if !foundWarning {
		t.Errorf("expected warning naming local_index_unavailable, got %v", resp.Warnings)
	}

	// Tool description should advertise the unavailable signal too.
	for _, tool := range s.Tools() {
		if tool.Name == "search_local_index" {
			if !strings.Contains(strings.ToLower(tool.Description), "local_index_unavailable") {
				t.Errorf("tool description should advertise local_index_unavailable, got %q", tool.Description)
			}
		}
	}
}

func TestSearchLocalIndexConfiguredProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	corpus := `{"version":1,"documents":[{"id":"guide","title":"Local search guide","snippet":"Configure a safe local corpus.","tags":["search"]}]}`
	if err := os.WriteFile(path, []byte(corpus), 0o600); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	localIndex, err := connectors.NewLocalIndexConnector(path)
	if err != nil {
		t.Fatalf("NewLocalIndexConnector: %v", err)
	}

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(localIndex)
	history := cache.NewHistoryService(10)
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", "", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), history, memory.NewClient("", ""))

	resp, err := s.callToolByName(context.Background(), "search_local_index", []byte(`{"query":"local search"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Strategy != "local_index_lexical" || len(resp.Results) != 1 {
		t.Fatalf("unexpected configured response: strategy=%q results=%d", resp.Strategy, len(resp.Results))
	}
	if resp.Results[0].CitationID != "local-index:guide" || !containsString(resp.SourcesUsed, "local_index") {
		t.Fatalf("missing local index provenance: %+v", resp)
	}
	entries := history.List(context.Background(), 1, "")
	if len(entries) != 1 || entries[0].Source != "local_index" {
		t.Fatalf("configured search was not recorded with local source: %+v", entries)
	}
}

// TestNormalizeResponseIsIdempotent covers the helper itself directly so a
// future regression in normalizeSearchResponse cannot leak nil slices
// back to clients. It also proves the helper is idempotent: calling it
// twice on the same response must not duplicate or grow any slice.
func TestNormalizeResponseIsIdempotent(t *testing.T) {
	r := normalizeSearchResponse(nil)
	if r == nil || r.Results == nil || len(r.Results) != 0 {
		t.Fatalf("expected normalizeSearchResponse(nil) to return a stable empty response, got %+v", r)
	}

	in := &types.SearchResponse{
		Query:       "x",
		Results:     nil,
		SourcesUsed: nil,
		Warnings:    nil,
		Errors:      nil,
		KeyFindings: nil,
	}
	out := normalizeSearchResponse(in)
	if out.Results == nil || out.SourcesUsed == nil || out.Warnings == nil || out.Errors == nil || out.KeyFindings == nil {
		t.Fatalf("normalize must replace every nil slice with []")
	}
	if len(out.Results) != 0 || len(out.SourcesUsed) != 0 || len(out.Warnings) != 0 || len(out.Errors) != 0 || len(out.KeyFindings) != 0 {
		t.Fatalf("normalize must produce empty (not nil) slices")
	}
	// Calling twice must not append or duplicate anything.
	out2 := normalizeSearchResponse(out)
	if len(out2.Results) != 0 || len(out2.SourcesUsed) != 0 || len(out2.Warnings) != 0 || len(out2.Errors) != 0 || len(out2.KeyFindings) != 0 {
		t.Fatalf("normalize must be idempotent")
	}
}
