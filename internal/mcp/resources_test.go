package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

// buildResourcesTestServer stands up a Server with the minimum wiring
// required to exercise resources/list and resources/read end-to-end.
// A non-nil auth validator is wired so the HTTP-boundary test can
// reach /mcp with the matching X-Api-Key header.
func buildResourcesTestServer(t *testing.T) *Server {
	t.Helper()
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector("http://localhost:9999", cacheSvc))
	return NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:9999", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), auth.NewValidator("test-key-resources"), observability.New(), cache.NewHistoryService(10))
}

// TestResourcesListAdvertisesAgentGuide locks the discoverability
// contract: resources/list must return exactly one entry whose URI
// matches the documented stable URI agent-guide://sourcerudder/wire-contract.
// Real MCP clients iterate the list to decide what to fetch, so the
// URI must be stable and advertised through the wire boundary.
func TestResourcesListAdvertisesAgentGuide(t *testing.T) {
	s := buildResourcesTestServer(t)
	if AgentGuideURI != "agent-guide://sourcerudder/wire-contract" {
		t.Fatalf("AgentGuideURI = %q; want SourceRudder URI", AgentGuideURI)
	}

	res, err := s.HandleResourcesList(context.Background(), nil)
	if err != nil {
		t.Fatalf("HandleResourcesList: %v", err)
	}
	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", res)
	}
	raw, ok := m["resources"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map[string]interface{}, got %T", m["resources"])
	}
	if len(raw) != 1 {
		t.Fatalf("expected exactly 1 resource registered, got %d", len(raw))
	}
	if got := raw[0]["uri"]; got != AgentGuideURI {
		t.Errorf("expected URI %q, got %v", AgentGuideURI, got)
	}
	if name, _ := raw[0]["name"].(string); !strings.Contains(name, "sourcerudder") {
		t.Errorf("expected name to mention sourcerudder, got %q", name)
	}
	if mt, _ := raw[0]["mimeType"].(string); mt != agentGuideMIMEType {
		t.Errorf("expected mimeType %q, got %q", agentGuideMIMEType, mt)
	}
	if desc, _ := raw[0]["description"].(string); desc == "" {
		t.Errorf("expected non-empty description, got empty")
	}
}

// TestResourcesReadReturnsGuide asserts that resources/read returns
// the wire contract guide keyed by the stable URI. The test verifies
// three invariants: (1) the contents envelope follows the MCP spec,
// (2) the URI echoes back, (3) the body is non-empty markdown.
func TestResourcesReadReturnsGuide(t *testing.T) {
	s := buildResourcesTestServer(t)

	res, err := s.HandleResourcesRead(context.Background(), json.RawMessage(`{"uri":"`+AgentGuideURI+`"}`))
	if err != nil {
		t.Fatalf("HandleResourcesRead: %v", err)
	}
	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", res)
	}
	contents, ok := m["contents"].([]map[string]interface{})
	if !ok || len(contents) != 1 {
		t.Fatalf("expected single-element contents array, got %T / %v", m["contents"], m["contents"])
	}
	body, _ := contents[0]["text"].(string)
	if body == "" {
		t.Fatalf("expected non-empty guide body")
	}
	if uri, _ := contents[0]["uri"].(string); uri != AgentGuideURI {
		t.Errorf("expected URI echo %q, got %q", AgentGuideURI, uri)
	}
	if mt, _ := contents[0]["mimeType"].(string); mt != agentGuideMIMEType {
		t.Errorf("expected mimeType %q in contents, got %q", agentGuideMIMEType, mt)
	}
}

// TestResourcesReadUnknownURIRejected verifies the failure path: an
// unknown URI must surface an error so clients do not silently treat
// an empty contents array as a successful lookup.
func TestResourcesReadUnknownURIRejected(t *testing.T) {
	s := buildResourcesTestServer(t)

	_, err := s.HandleResourcesRead(context.Background(), json.RawMessage(`{"uri":"agent-guide://sourcerudder/does-not-exist"}`))
	if err == nil {
		t.Fatalf("expected error for unknown URI, got nil")
	}
	if !strings.Contains(err.Error(), "resource not found") {
		t.Errorf("expected 'resource not found' error, got %v", err)
	}
}

// TestResourcesReadEmptyURIRefused covers the second failure mode: a
// missing URI must fail loudly rather than matching the first resource.
func TestResourcesReadEmptyURIRefused(t *testing.T) {
	s := buildResourcesTestServer(t)

	for _, payload := range []string{`{}`, `{"uri":""}`, `{"uri":"   "}`} {
		t.Run(payload, func(t *testing.T) {
			if _, err := s.HandleResourcesRead(context.Background(), json.RawMessage(payload)); err == nil {
				t.Errorf("expected error for payload %s, got nil", payload)
			}
		})
	}
}

// TestAgentGuideCoversWireContractContent locks the deliverable:
// every topic the user asked for must be present, with the strategy
// IDs the connectors emit and the field names agents will see. If a
// future contributor edits the guide and accidentally drops a key
// section, this test fires before the change ships.
func TestAgentGuideCoversWireContractContent(t *testing.T) {
	body := wireContractGuide

	requiredSubstrings := []string{
		// Tool family names and the five families.
		"Búsqueda", "Fetch/extract", "Validación", "Síntesis", "Tiempo",
		// Stable SearchResponse fields.
		"results", "strategy", "partial", "warnings", "errors", "cached",
		// Distinguishing healthy empty vs degraded vs unconfigured.
		"healthy", "degradado", "local_index_unavailable", "official_doc_web_fallback", "searxng_reddit_index",
		// Specialized tool limitations.
		"search_doc_oficial", "search_local_index",
		// Output shapes for each family.
		"FetchResponse", "valid", "summarize_results", "deep_research", "compare_sources", "get_current_date",
		// Stable URI for re-reading the guide itself.
		AgentGuideURI,
		// SearchResultItem contract.
		"SearchResultItem", "citationId",
	}

	for _, want := range requiredSubstrings {
		if !strings.Contains(body, want) {
			t.Errorf("wire-contract guide must mention %q to be useful to agents", want)
		}
	}

	// Language contract: the guide MUST be Spanish. A regression that
	// flips it to English (or to mix languages inside a paragraph)
	// would defeat the point of the resource, since the operator-
	// facing contract is supposed to be Spanish by design.
	if !strings.Contains(body, "Esta guía") {
		t.Errorf("guide must open with the Spanish framing sentence")
	}
	if strings.Contains(body, "This guide describes") {
		t.Errorf("guide must NOT contain English framing; got English sentence")
	}
}

// TestAgentGuideCallsOutNonFunctionalInputs is the regression guard
// against future "let's add this knob" suggestions that would expand
// the synthesis or fetch schemas with fields the code doesn't honor.
// The guide explicitly tells agents that style/goal and timeoutMs are
// not read; if a future edit drops the warning, agents will waste
// tokens sending values the server discards.
func TestAgentGuideCallsOutNonFunctionalInputs(t *testing.T) {
	body := wireContractGuide

	mustMention := []string{
		// style / goal in synthesis are not implemented.
		"\"style\"", "\"goal\"",
		// timeoutMs in fetch is not implemented (server has its own
		// --fetch-timeout-ms boot-time flag).
		"timeoutMs",
	}

	for _, want := range mustMention {
		if !strings.Contains(body, want) {
			t.Errorf("guide must call out non-functional input %q so agents stop sending it", want)
		}
	}
}

// TestInitializeAdvertisesResourcesCapability locks the contract that
// the initialize response advertises the resources capability so MCP
// clients know they can issue resources/list. Without this, an MCP
// client that gates discovery on the capabilities map would never
// reach the resource.
func TestInitializeAdvertisesResourcesCapability(t *testing.T) {
	s := buildResourcesTestServer(t)

	res, err := s.HandleInitialize(context.Background(), json.RawMessage(`{"clientId":"test","protocolVersion":"2024-11-05"}`))
	if err != nil {
		t.Fatalf("HandleInitialize: %v", err)
	}
	m, ok := res.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map result, got %T", res)
	}
	caps, ok := m["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected capabilities map, got %T", m["capabilities"])
	}
	tools, ok := caps["tools"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected tools capability map, got %T", caps["tools"])
	}
	if _, hasListChanged := tools["listChanged"]; !hasListChanged {
		t.Errorf("expected tools.listChanged in capability map")
	}
	resources, ok := caps["resources"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected resources capability map, got %T", caps["resources"])
	}
	if _, hasListChanged := resources["listChanged"]; !hasListChanged {
		t.Errorf("expected resources.listChanged in capability map")
	}
}

// TestResourcesListAndReadThroughHTTPBoundary is the end-to-end proof:
// the agent guide must be reachable through the real HTTP boundary
// (the same JSON-RPC /mcp endpoint production serves), not just an
// in-process method call. The boundary catches any regression in the
// router that would route resources/* to the wrong handler.
func TestResourcesListAndReadThroughHTTPBoundary(t *testing.T) {
	s := buildResourcesTestServer(t)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	// resources/list over the JSON-RPC boundary.
	listPayload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "resources/list",
	})
	resp, err := postWithKey(srv.URL+"/mcp", "application/json", listPayload, "test-key-resources")
	if err != nil {
		t.Fatalf("POST resources/list: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /mcp, got %d: %s", resp.StatusCode, body)
	}
	var listRPC map[string]interface{}
	if err := json.Unmarshal(body, &listRPC); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	result, ok := listRPC["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result envelope, got %v", listRPC)
	}
	resources, ok := result["resources"].([]interface{})
	if !ok || len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %v", result["resources"])
	}
	firstResource := resources[0].(map[string]interface{})
	if firstResource["uri"] != AgentGuideURI {
		t.Errorf("expected URI %q, got %v", AgentGuideURI, firstResource["uri"])
	}

	// resources/read over the same boundary.
	readPayload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "resources/read",
		"params":  map[string]interface{}{"uri": AgentGuideURI},
	})
	resp2, err := postWithKey(srv.URL+"/mcp", "application/json", readPayload, "test-key-resources")
	if err != nil {
		t.Fatalf("POST resources/read: %v", err)
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /mcp on resources/read, got %d: %s", resp2.StatusCode, body2)
	}
	var readRPC map[string]interface{}
	if err := json.Unmarshal(body2, &readRPC); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	readResult, ok := readRPC["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result envelope on read, got %v", readRPC)
	}
	contents, ok := readResult["contents"].([]interface{})
	if !ok || len(contents) != 1 {
		t.Fatalf("expected single-element contents array, got %v", readResult["contents"])
	}
	text, _ := contents[0].(map[string]interface{})["text"].(string)
	if !strings.Contains(text, "Wire Contract") {
		t.Errorf("expected guide to contain 'Wire Contract', got first 80 chars: %q", text[:min(80, len(text))])
	}
	if !strings.Contains(text, AgentGuideURI) {
		t.Errorf("expected guide to mention its own URI %q", AgentGuideURI)
	}

	// Negative path: unknown URI returns a JSON-RPC error envelope,
	// not an empty success. This matters because an MCP client that
	// sees an empty array would happily cache "no resource here".
	badPayload, _ := json.Marshal(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "resources/read",
		"params":  map[string]interface{}{"uri": "agent-guide://sourcerudder/does-not-exist"},
	})
	resp3, err := postWithKey(srv.URL+"/mcp", "application/json", badPayload, "test-key-resources")
	if err != nil {
		t.Fatalf("POST resources/read unknown: %v", err)
	}
	defer resp3.Body.Close()
	body3, _ := io.ReadAll(resp3.Body)
	var errRPC map[string]interface{}
	if err := json.Unmarshal(body3, &errRPC); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	errEnv, ok := errRPC["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected error envelope on unknown URI, got %v", errRPC)
	}
	if !strings.Contains(errEnv["message"].(string), "resource not found") {
		t.Errorf("expected 'resource not found' message, got %v", errEnv)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// postWithKey wraps http.Post so the auth X-Api-Key header is carried
// on every JSON-RPC boundary call. /mcp is wrapped by the auth
// middleware after the Phase 12 hardening, so any test that drives
// the wire directly MUST use this helper instead of http.Post.
func postWithKey(url, contentType string, body []byte, key string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Api-Key", key)
	return http.DefaultClient.Do(req)
}
