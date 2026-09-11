package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/auth"
	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/connectors"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
)

// TestDegradedSearXNGExposesMetricOverHTTP is the end-to-end regression
// guard for the production bug where /metrics served a different
// registry than the one connectors incremented. It wires the production
// metrics instance into both surfaces (observability.SetDefault and the
// MCP server), stands the actual Server.Handler() up on an ephemeral
// port via httptest.NewServer, drives a degraded SearXNG response
// through the real JSON-RPC /mcp endpoint, and proves the
// sourcerudder_search_degraded_total counter is visible at /metrics.
//
// Before the fix this test would scrape /metrics after the tool call
// and find no sourcerudder_search_degraded_total sample, because the
// connector would have incremented a separate Metrics instance from
// the one the Server.Handler() exposed.
func TestDegradedSearXNGExposesMetricOverHTTP(t *testing.T) {
	const uniqueQuery = "degraded-e2e-scrape-unique-query"

	// 1. SearXNG mock returns the canonical degraded body: 200 OK with
	// empty results and a non-empty unresponsive_engines array. The
	// WebConnector is the only consumer of this body in SourceRudder's
	// registry, so we know exactly which counter the test must tick.
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[["duckduckgo","timeout"]]}`))
	}))
	defer searxng.Close()

	// 2. One Metrics instance, wired into both surfaces the bug
	// previously split. SetDefault is what the connector path reads
	// (it calls observability.Default().RecordSearchDegraded); the
	// same pointer is what the Server's /metrics endpoint serves.
	met := observability.New()
	observability.SetDefault(met)
	t.Cleanup(func() { observability.SetDefault(nil) })

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(searxng.URL, cacheSvc))

	// Wire an auth validator with a known key. The /mcp and
	// /metrics endpoints are wrapped in the auth middleware, so
	// every request below carries X-Api-Key to reach the handler.
	authVal := auth.NewValidator("test-key-e2e")

	s := NewServer(cm, search.NewPlanner(), "http", ":0", searxng.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), authVal, met, cache.NewHistoryService(10))

	// 3. The actual production HTTP boundary. s.Handler() is the same
	// chain HTTPTransport.Start serves on a real port; wrapping it in
	// httptest.NewServer gives an ephemeral port + the same mux
	// without binding to localhost:8080.
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// 4. Pre-condition: scrape /metrics before driving any traffic.
	// The counter must not yet exist (or be 0). This catches a future
	// regression where a leftover default leaks across tests and
	// gives a false-positive pass.
	preBody := scrapeHTTP(t, srv.URL+"/metrics")
	if counterValue(preBody, "sourcerudder_search_degraded_total", "web", "unresponsive_engines") != 0 {
		t.Fatalf("expected degraded counter to start at 0, scrape body:\n%s", preBody)
	}

	// 5. Drive the degraded SearXNG path through the real JSON-RPC
	// /mcp endpoint. The unique query avoids any in-process cache
	// poisoning across test runs.
	rpcPayload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "search_web",
			"arguments": map[string]interface{}{
				"query": uniqueQuery,
			},
		},
	}
	rpcBody, err := json.Marshal(rpcPayload)
	if err != nil {
		t.Fatalf("marshal rpc payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", bytes.NewReader(rpcBody))
	if err != nil {
		t.Fatalf("build /mcp request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", "test-key-e2e")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK from /mcp, got %d: %s", resp.StatusCode, body)
	}
	var rpcResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("decode rpc response: %v", err)
	}
	if _, ok := rpcResp["result"]; !ok {
		t.Fatalf("expected rpc result envelope, got %v", rpcResp)
	}

	// 6. Post-condition: the same /metrics endpoint that production
	// scrapes must now show the counter. The label ordering on the
	// wire is alphabetical (kind, source) per Prometheus convention,
	// but counterValue accepts either order so a future client-side
	// reshuffle does not break this regression guard. If the
	// connector had incremented a different Metrics instance than
	// the one /metrics serves (the exact production bug), this
	// assertion would fail with got=0.
	body := scrapeHTTP(t, srv.URL+"/metrics")
	if !strings.Contains(body, "sourcerudder_search_degraded_total") {
		t.Fatalf("expected sourcerudder_search_degraded_total in /metrics body, got:\n%s", body)
	}
	if got := counterValue(body, "sourcerudder_search_degraded_total", "web", "unresponsive_engines"); got < 1 {
		t.Fatalf("expected sourcerudder_search_degraded_total{web,unresponsive_engines} >= 1, got %d, body:\n%s", got, body)
	}
}

// scrapeHTTP fetches the response body at a URL and returns it as a
// string. It does not interpret the body; the assertions are made by
// the counterValue helper below so the parsing rules stay local to
// each assertion. /metrics is auth-protected under the post-fix
// contract, so the helper carries the test key.
func scrapeHTTP(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("scrape %s: build request: %v", url, err)
	}
	req.Header.Set("X-Api-Key", "test-key-e2e")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("scrape %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("scrape %s: expected 200, got %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("scrape %s: read body: %v", url, err)
	}
	return string(b)
}

// counterValue finds the metric line for the given (source, kind) pair
// and returns its numeric value, or 0 if it is absent. It accepts
// either label order because Prometheus emits labels in alphabetical
// order regardless of how the metric was registered, and the test
// should not be coupled to that detail.
func counterValue(body, metric, source, kind string) int {
	prefixes := []string{
		metric + `{source="` + source + `",kind="` + kind + `"}`,
		metric + `{kind="` + kind + `",source="` + source + `"}`,
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(line, prefix) {
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				n, err := strconv.Atoi(fields[1])
				if err != nil {
					return 0
				}
				return n
			}
		}
	}
	return 0
}
