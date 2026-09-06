package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thiscloud/ia-buscar/internal/auth"
	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/connectors"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/memory"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

// TestGetCachedHitReturnsEntry covers the cache-hit branch of
// get_cached: when the cache holds a non-expired entry under `key`,
// the handler MUST return JSON of the shape
// {"cache_hit":true,"entry":<CacheEntry>} with no error. Triangulates
// against the miss path so a regression that always returns
// cache_hit=false surfaces here.
func TestGetCachedHitReturnsEntry(t *testing.T) {
	cacheSvc := cache.NewService(60)
	srv := buildToolsTestServer(t, cacheSvc)

	// Seed the cache so a hit is guaranteed.
	if err := cacheSvc.Set(context.Background(), "alpha", []byte("PAYLOAD-A"), []string{"web"}); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	res, err := callToolByNameWithCtx(srv, "get_cached", map[string]interface{}{"key": "alpha"})
	if err != nil {
		t.Fatalf("get_cached hit: %v", err)
	}
	m := res.(map[string]interface{})
	if hit, _ := m["cache_hit"].(bool); !hit {
		t.Fatalf("expected cache_hit=true, got %#v", m["cache_hit"])
	}
	entry, ok := m["entry"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected entry to be a JSON object, got %T (%#v)", m["entry"], m["entry"])
	}
	if entry["cacheKey"] != "alpha" {
		t.Fatalf("expected entry.cacheKey=alpha, got %#v", entry["cacheKey"])
	}
	if payload, _ := entry["payload"].(string); payload != "PAYLOAD-A" {
		t.Fatalf("expected entry.payload=PAYLOAD-A, got %q", payload)
	}
}

// TestGetCachedMissReturnsEmpty covers the cache-miss branch of
// get_cached: when no entry (or only an expired entry) exists under
// `key`, the handler MUST return JSON of the shape
// {"cache_hit":false,"entry":null} with no error. The handler must
// NOT bubble up an error.
func TestGetCachedMissReturnsEmpty(t *testing.T) {
	cacheSvc := cache.NewService(60)
	srv := buildToolsTestServer(t, cacheSvc)

	res, err := callToolByNameWithCtx(srv, "get_cached", map[string]interface{}{"key": "missing"})
	if err != nil {
		t.Fatalf("get_cached miss: %v", err)
	}
	m := res.(map[string]interface{})
	if hit, _ := m["cache_hit"].(bool); hit {
		t.Fatalf("expected cache_hit=false, got %#v", m["cache_hit"])
	}
	if entry := m["entry"]; entry != nil {
		t.Fatalf("expected entry=null on miss, got %#v", entry)
	}
}

// TestInvalidateCachePresent covers the present path: after Set,
// invalidate_cache(key) MUST remove the entry and return
// {"key":"K","invalidated":true} with no error. A follow-up Get MUST
// miss.
func TestInvalidateCachePresent(t *testing.T) {
	cacheSvc := cache.NewService(60)
	srv := buildToolsTestServer(t, cacheSvc)

	if err := cacheSvc.Set(context.Background(), "k", []byte("v"), nil); err != nil {
		t.Fatalf("seed Set: %v", err)
	}

	res, err := callToolByNameWithCtx(srv, "invalidate_cache", map[string]interface{}{"key": "k"})
	if err != nil {
		t.Fatalf("invalidate_cache present: %v", err)
	}
	m := res.(map[string]interface{})
	if inv, _ := m["invalidated"].(bool); !inv {
		t.Fatalf("expected invalidated=true, got %#v", m["invalidated"])
	}
	if k, _ := m["key"].(string); k != "k" {
		t.Fatalf("expected key=k, got %q", k)
	}
	if _, ok, _ := cacheSvc.Get(context.Background(), "k"); ok {
		t.Fatal("expected key to be gone after invalidate_cache present")
	}
}

// TestInvalidateCacheMissingIdempotent covers the absent path:
// invalidate_cache(key) on a key that is not present MUST return
// {"key":"K","invalidated":false} with no error. The operation MUST
// NOT bubble up an error and MUST NOT panic.
func TestInvalidateCacheMissingIdempotent(t *testing.T) {
	cacheSvc := cache.NewService(60)
	srv := buildToolsTestServer(t, cacheSvc)

	res, err := callToolByNameWithCtx(srv, "invalidate_cache", map[string]interface{}{"key": "nope"})
	if err != nil {
		t.Fatalf("invalidate_cache missing: %v", err)
	}
	m := res.(map[string]interface{})
	if inv, _ := m["invalidated"].(bool); inv {
		t.Fatalf("expected invalidated=false, got %#v", m["invalidated"])
	}
	if k, _ := m["key"].(string); k != "nope" {
		t.Fatalf("expected key=nope, got %q", k)
	}
}

// TestGetSearchHistoryWithinLimitNewestFirst covers the limit +
// newest-first contract: when the HistoryService retains more
// entries than the requested limit, the handler MUST return at most
// `limit` entries newest-first in JSON shape
// {"history":[...],"limit":N,"query":"..."}.
func TestGetSearchHistoryWithinLimitNewestFirst(t *testing.T) {
	history := cache.NewHistoryService(10)
	srv := buildToolsTestServerWithHistory(t, history)

	// Append 5 entries oldest-first.
	for _, q := range []string{"a", "b", "c", "d", "e"} {
		if err := history.Append(context.Background(), cache.Entry{Query: q, Source: "web"}); err != nil {
			t.Fatalf("Append %s: %v", q, err)
		}
	}

	res, err := callToolByNameWithCtx(srv, "get_search_history", map[string]interface{}{"limit": 3})
	if err != nil {
		t.Fatalf("get_search_history: %v", err)
	}
	m := res.(map[string]interface{})
	if lim, _ := m["limit"].(float64); int(lim) != 3 {
		t.Fatalf("expected limit=3, got %#v", m["limit"])
	}
	historyArr, ok := m["history"].([]interface{})
	if !ok {
		t.Fatalf("expected history to be an array, got %T", m["history"])
	}
	if len(historyArr) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(historyArr))
	}
	first := historyArr[0].(map[string]interface{})
	if first["query"] != "e" {
		t.Fatalf("expected newest-first (e first), got %#v", first["query"])
	}
	last := historyArr[2].(map[string]interface{})
	if last["query"] != "c" {
		t.Fatalf("expected limit clamp (c last), got %#v", last["query"])
	}
}

// TestGetSearchHistoryQuerySubstringFilter covers the substring
// narrowing: when `query` is supplied, only entries whose stored
// Query contains that substring are returned. Triangulates the
// empty-query path so a regression that always filters (or never
// filters) surfaces here.
func TestGetSearchHistoryQuerySubstringFilter(t *testing.T) {
	history := cache.NewHistoryService(10)
	srv := buildToolsTestServerWithHistory(t, history)

	for _, q := range []string{"go parse", "go fetch", "python scrape", "go render"} {
		if err := history.Append(context.Background(), cache.Entry{Query: q, Source: "web"}); err != nil {
			t.Fatalf("Append %q: %v", q, err)
		}
	}

	res, err := callToolByNameWithCtx(srv, "get_search_history", map[string]interface{}{
		"limit": 10,
		"query": "go",
	})
	if err != nil {
		t.Fatalf("get_search_history filter: %v", err)
	}
	m := res.(map[string]interface{})
	if q, _ := m["query"].(string); q != "go" {
		t.Fatalf("expected query=go, got %q", q)
	}
	historyArr := m["history"].([]interface{})
	if len(historyArr) != 3 {
		t.Fatalf("expected 3 entries containing 'go', got %d", len(historyArr))
	}
	for _, raw := range historyArr {
		entry := raw.(map[string]interface{})
		q, _ := entry["query"].(string)
		if !contains(q, "go") {
			t.Fatalf("entry %q does not contain substring 'go'", q)
		}
	}
}

// TestSearchWebAppendsToSearchHistory is the corrective-retry behavior-first
// RED test for the get_search_history wiring gap: a real production
// search_web call (driven through the MCP HandleToolsCall boundary against a
// stub SearxNG httptest server) MUST result in a HistoryService entry that
// get_search_history returns. Triangulates against direct Append so a
// regression that only records on cache hit surfaces here. The assertion is
// non-trivial: it requires the handler to invoke s.history.Append with the
// exact query string and source the operator passed.
func TestSearchWebAppendsToSearchHistory(t *testing.T) {
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[{"title":"t","url":"https://example.com/x","content":"c"}],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	history := cache.NewHistoryService(10)
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(searxng.URL, cacheSvc))
	srv := buildToolsTestServerWithHistoryAndConnectors(t, history, cacheSvc, cm, searxng.URL)

	// Drive the real production path.
	if _, err := callToolByNameWithCtx(srv, "search_web", map[string]interface{}{"query": "go fetch"}); err != nil {
		t.Fatalf("search_web: unexpected error: %v", err)
	}

	// Now read the history and assert the entry is present.
	res, err := callToolByNameWithCtx(srv, "get_search_history", map[string]interface{}{"limit": 10})
	if err != nil {
		t.Fatalf("get_search_history: %v", err)
	}
	m := res.(map[string]interface{})
	arr, ok := m["history"].([]interface{})
	if !ok {
		t.Fatalf("expected history array, got %T (%#v)", m["history"], m["history"])
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 history entry after one search_web call, got %d (%#v)", len(arr), arr)
	}
	entry := arr[0].(map[string]interface{})
	if entry["query"] != "go fetch" {
		t.Fatalf("expected recorded query=go fetch, got %#v", entry["query"])
	}
	if entry["source"] != "web" {
		t.Fatalf("expected recorded source=web, got %#v", entry["source"])
	}
}

// TestSearchGitHubPRAppendsToSearchHistory is the corrective-retry TRIANGULATE
// case for the same wiring gap. search_github_pr follows a different code
// path (it routes through GitHubConnector.SearchPR rather than
// ConnectorManager.Search). Both paths MUST record into the same
// HistoryService so a regression that only wires one source surfaces here.
func TestSearchGitHubPRAppendsToSearchHistory(t *testing.T) {
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Not used by search_github_pr; registered so ConnectorManager
		// has a connector for completeness. The PR call resolves the
		// "github" connector directly via GetConnector.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	history := cache.NewHistoryService(10)
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewGitHubConnector("", cacheSvc))
	srv := buildToolsTestServerWithHistoryAndConnectors(t, history, cacheSvc, cm, searxng.URL)

	// The anonymous connector still performs HTTP. Stub the default transport
	// for this sequential test so it drives the real uncached SearchPR path
	// without DNS, TLS, or a live GitHub dependency.
	originalTransport := http.DefaultTransport
	transport := &githubPRHistoryTransport{t: t}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	if _, err := callToolByNameWithCtx(srv, "search_github_pr", map[string]interface{}{"query": "Add HistoryService wiring"}); err != nil {
		t.Fatalf("search_github_pr: unexpected error: %v", err)
	}
	if transport.requests != 1 {
		t.Fatalf("expected one uncached GitHub request, got %d", transport.requests)
	}

	res, err := callToolByNameWithCtx(srv, "get_search_history", map[string]interface{}{"limit": 10})
	if err != nil {
		t.Fatalf("get_search_history: %v", err)
	}
	m := res.(map[string]interface{})
	arr := m["history"].([]interface{})
	if len(arr) != 1 {
		t.Fatalf("expected 1 history entry after one search_github_pr call, got %d (%#v)", len(arr), arr)
	}
	entry := arr[0].(map[string]interface{})
	if entry["query"] != "Add HistoryService wiring" {
		t.Fatalf("expected recorded query=Add HistoryService wiring, got %#v", entry["query"])
	}
	if entry["source"] != "github" {
		t.Fatalf("expected recorded source=github, got %#v", entry["source"])
	}
}

type githubPRHistoryTransport struct {
	t        *testing.T
	requests int
}

func (t *githubPRHistoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.t.Helper()
	t.requests++
	if req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "api.github.com" || req.URL.Path != "/search/issues" {
		t.t.Fatalf("unexpected outbound request: %s %s", req.Method, req.URL)
	}
	query := req.URL.Query()
	if query.Get("q") != "Add HistoryService wiring is:pr" || query.Get("state") != "open" || query.Get("per_page") != "10" {
		t.t.Fatalf("unexpected GitHub PR query: %q", req.URL.RawQuery)
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"items":[]}`)),
		Request:    req,
	}, nil
}

// TestSearchFailureProducesNoHistoryEntry is the second corrective-retry
// negative-path behavior-first RED test for the search-to-history wiring.
// It drives a real production search_web call (through the MCP
// HandleToolsCall boundary) against an httptest SearxNG stub that
// returns HTTP 500. The WebConnector surfaces this as a degraded
// response: `Partial=true`, `Results=[]`, no error returned to the
// handler. The corrected recordSearch MUST skip Append when the
// response is degraded AND empty — a failed search must NOT pollute
// the operator's history (the operator saw an empty result, not a
// search they completed). Triangulates TestSearchWebAppendsToSearchHistory:
// a regression that always records (the pre-fix behavior) surfaces here.
func TestSearchFailureProducesNoHistoryEntry(t *testing.T) {
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	history := cache.NewHistoryService(10)
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(searxng.URL, cacheSvc))
	srv := buildToolsTestServerWithHistoryAndConnectors(t, history, cacheSvc, cm, searxng.URL)

	// Drive the real production path with the failing upstream.
	// The connector swallows the 500 into a degraded response, so
	// the handler returns (resp, nil) — recordSearch is the gate.
	if _, err := callToolByNameWithCtx(srv, "search_web", map[string]interface{}{"query": "deliberately failing query"}); err != nil {
		t.Fatalf("search_web: unexpected error: %v (handler should swallow degraded response)", err)
	}

	// A failed search MUST NOT produce a HistoryService entry.
	if got := history.Len(); got != 0 {
		t.Fatalf("expected 0 history entries after a failed (HTTP 500) search, got %d", got)
	}

	// Independently confirm the gate is observable through the public
	// tool surface: get_search_history returns an empty array.
	res, err := callToolByNameWithCtx(srv, "get_search_history", map[string]interface{}{"limit": 10})
	if err != nil {
		t.Fatalf("get_search_history: %v", err)
	}
	m := res.(map[string]interface{})
	arr, ok := m["history"].([]interface{})
	if !ok {
		t.Fatalf("expected history array, got %T (%#v)", m["history"], m["history"])
	}
	if len(arr) != 0 {
		t.Fatalf("expected empty history list after a failed search, got %#v", arr)
	}
}

// TestSearchDocOficialRecordsHistory is the second corrective-retry positive
// RED→GREEN-on-first-run test for the search-to-history wiring. It drives a
// real production search_doc_oficial call (through the MCP HandleToolsCall
// boundary) against an httptest SearxNG stub that returns valid JSON. The
// tool falls back to the web connector (search_doc_oficial has no
// specialized official-doc provider wired today — see
// makeDocOficialHandler) and the corrected recordSearch MUST Append the
// entry. Triangulates TestSearchWebAppendsToSearchHistory: the makeDocOficial
// path is a distinct handler and a distinct connector name from search_web,
// so a regression that wires only one of the two surfaces here.
func TestSearchDocOficialRecordsHistory(t *testing.T) {
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[{"title":"t","url":"https://example.com/x","content":"c"}],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	history := cache.NewHistoryService(10)
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(searxng.URL, cacheSvc))
	srv := buildToolsTestServerWithHistoryAndConnectors(t, history, cacheSvc, cm, searxng.URL)

	if _, err := callToolByNameWithCtx(srv, "search_doc_oficial", map[string]interface{}{"query": "go documentation"}); err != nil {
		t.Fatalf("search_doc_oficial: unexpected error: %v", err)
	}

	res, err := callToolByNameWithCtx(srv, "get_search_history", map[string]interface{}{"limit": 10})
	if err != nil {
		t.Fatalf("get_search_history: %v", err)
	}
	m := res.(map[string]interface{})
	arr, ok := m["history"].([]interface{})
	if !ok {
		t.Fatalf("expected history array, got %T (%#v)", m["history"], m["history"])
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 history entry after one search_doc_oficial call, got %d (%#v)", len(arr), arr)
	}
	entry := arr[0].(map[string]interface{})
	if entry["query"] != "go documentation" {
		t.Fatalf("expected recorded query=go documentation, got %#v", entry["query"])
	}
	// search_doc_oficial falls back to the web connector today; the
	// handler records the connector name that actually answered (web),
	// not the tool name. This is the same convention as the simple
	// search_* handlers and the GitHub handlers.
	if entry["source"] != "web" {
		t.Fatalf("expected recorded source=web (fallback connector), got %#v", entry["source"])
	}
}

// TestSearchGitHubIssueRecordsHistory is the second corrective-retry
// positive RED→GREEN-on-first-run test for the search-to-history wiring.
// It drives a real production search_github_issue call (through the MCP
// HandleToolsCall boundary) using the production GitHubConnector (which
// points at https://api.github.com by construction — there is no public
// hook to override baseURL, so we exercise the live endpoint exactly like
// the W4 TestSearchGitHubPRAppendsToSearchHistory test does). The
// corrected recordSearch MUST Append the entry. Triangulates
// TestSearchGitHubPRAppendsToSearchHistory: the makeGitHubIssueHandler
// path is a distinct handler that calls ghConn.SearchIssue (not
// ghConn.SearchPR) and a regression that wires only the PR handler
// surfaces here. Like the PR test, this depends on api.github.com
// returning a parseable response for the chosen query; the connector
// swallows any transport / decode error into a degraded response, and
// the corrected recordSearch skips degraded entries. We therefore pick
// a query the live API reliably answers with at least one issue.
func TestSearchGitHubIssueRecordsHistory(t *testing.T) {
	history := cache.NewHistoryService(10)
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewGitHubConnector("", cacheSvc))
	srv := buildToolsTestServerWithHistoryAndConnectors(t, history, cacheSvc, cm, "http://localhost:8888")

	// Use a query the live GitHub API reliably answers with issues.
	// "memory" is broad enough to return results without auth.
	if _, err := callToolByNameWithCtx(srv, "search_github_issue", map[string]interface{}{"query": "memory"}); err != nil {
		t.Fatalf("search_github_issue: unexpected error: %v", err)
	}

	res, err := callToolByNameWithCtx(srv, "get_search_history", map[string]interface{}{"limit": 10})
	if err != nil {
		t.Fatalf("get_search_history: %v", err)
	}
	m := res.(map[string]interface{})
	arr, ok := m["history"].([]interface{})
	if !ok {
		t.Fatalf("expected history array, got %T (%#v)", m["history"], m["history"])
	}
	// The handler records the entry unconditionally on the production
	// search path (before this corrective batch, even degraded entries
	// were recorded; after the fix, only non-degraded entries are
	// recorded). The query "memory" reliably returns issues on the live
	// API, so the response is non-degraded and the entry is present.
	if len(arr) != 1 {
		t.Fatalf("expected 1 history entry after one search_github_issue call, got %d (%#v)", len(arr), arr)
	}
	entry := arr[0].(map[string]interface{})
	if entry["query"] != "memory" {
		t.Fatalf("expected recorded query=memory, got %#v", entry["query"])
	}
	if entry["source"] != "github" {
		t.Fatalf("expected recorded source=github, got %#v", entry["source"])
	}
}

// TestGetCachedExpiredEntryReturnsEmpty covers the corrective-retry expired
// branch of get_cached: when an entry IS present in the cache under `key`
// but its TTL has already elapsed, the handler MUST return
// {"cache_hit":false,"entry":null} with no error. Triangulates the existing
// missing-key test so a regression that always returns cache_hit=true for
// any present key (regardless of expiry) surfaces here.
func TestGetCachedExpiredEntryReturnsEmpty(t *testing.T) {
	// TTL=0 means expires == created; combined with a tiny sleep we
	// get a deterministic expired entry without adding test-only
	// surface to the cache package.
	cacheSvc := cache.NewService(0)
	srv := buildToolsTestServer(t, cacheSvc)

	if err := cacheSvc.Set(context.Background(), "expired", []byte("STALE-PAYLOAD"), []string{"web"}); err != nil {
		t.Fatalf("seed Set: %v", err)
	}
	// Give the monotonic clock a deterministic nudge past the
	// expires timestamp. 1ms is well below human-perceptible but
	// orders of magnitude larger than Go's clock resolution.
	time.Sleep(1 * time.Millisecond)

	res, err := callToolByNameWithCtx(srv, "get_cached", map[string]interface{}{"key": "expired"})
	if err != nil {
		t.Fatalf("get_cached expired: %v", err)
	}
	m := res.(map[string]interface{})
	if hit, _ := m["cache_hit"].(bool); hit {
		t.Fatalf("expected cache_hit=false for expired entry, got %#v (entry=%#v)", m["cache_hit"], m["entry"])
	}
	if entry := m["entry"]; entry != nil {
		t.Fatalf("expected entry=null for expired entry, got %#v", entry)
	}
	// And the entry must have been physically removed by the cache
	// service during its expiry sweep, not just hidden from this
	// caller — invalidate_cache on the same key now reports false.
	inv, err := callToolByNameWithCtx(srv, "invalidate_cache", map[string]interface{}{"key": "expired"})
	if err != nil {
		t.Fatalf("invalidate_cache post-expiry: %v", err)
	}
	invM := inv.(map[string]interface{})
	if invalidated, _ := invM["invalidated"].(bool); invalidated {
		t.Fatalf("expected invalidated=false on a key already removed by expiry sweep, got %#v", invM)
	}
}

// helpers ------------------------------------------------------------------

func buildToolsTestServer(t *testing.T, cacheSvc *cache.Service) *Server {
	t.Helper()
	cm := search.NewConnectorManager(cacheSvc)
	// Register a connector so /search_web has somewhere to route if
	// the test triggers it indirectly. No HTTP path is exercised here.
	cm.Register(connectors.NewWebConnector("http://localhost:8888", cacheSvc))
	return NewServer(
		cm, search.NewPlanner(),
		"stdio", ":8080", "http://localhost:8888",
		60, 5000,
		fetch.NewFetcherService(5000),
		synthesis.NewService(),
		auth.NewValidator("k"),
		observability.New(),
		cache.NewHistoryService(10), memory.NewClient("", ""),
	)
}

func buildToolsTestServerWithHistory(t *testing.T, history *cache.HistoryService) *Server {
	t.Helper()
	cacheSvc := cache.NewService(60)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector("http://localhost:8888", cacheSvc))
	return NewServer(
		cm, search.NewPlanner(),
		"stdio", ":8080", "http://localhost:8888",
		60, 5000,
		fetch.NewFetcherService(5000),
		synthesis.NewService(),
		auth.NewValidator("k"),
		observability.New(),
		history, memory.NewClient("", ""),
	)
}

// buildToolsTestServerWithHistoryAndConnectors wires a Server with the
// caller-provided history service, cache service, and already-populated
// connector manager. The corrective-retry search-to-history tests need
// control over the connector URL (so they can point a WebConnector at an
// httptest SearxNG stub) and over the HistoryService instance (so they
// can assert against it).
func buildToolsTestServerWithHistoryAndConnectors(t *testing.T, history *cache.HistoryService, _ *cache.Service, cm *search.ConnectorManager, searxngURL string) *Server {
	t.Helper()
	return NewServer(
		cm, search.NewPlanner(),
		"stdio", ":8080", searxngURL,
		60, 5000,
		fetch.NewFetcherService(5000),
		synthesis.NewService(),
		auth.NewValidator("k"),
		observability.New(),
		history, memory.NewClient("", ""),
	)
}

func callToolByNameWithCtx(srv *Server, name string, args map[string]interface{}) (interface{}, error) {
	argsJSON, _ := json.Marshal(args)
	params, _ := json.Marshal(map[string]interface{}{
		"name":      name,
		"arguments": json.RawMessage(argsJSON),
	})
	res, err := srv.HandleToolsCall(context.Background(), params)
	if err != nil {
		return nil, err
	}
	// Unwrap the standard JSON-RPC content/text envelope so tests can
	// assert the actual handler payload.
	m, ok := res.(map[string]interface{})
	if !ok {
		return res, nil
	}
	content, ok := m["content"].([]map[string]interface{})
	if !ok || len(content) == 0 {
		return res, nil
	}
	text, _ := content[0]["text"].(string)
	var decoded interface{}
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return text, nil
	}
	return decoded, nil
}

func contains(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
