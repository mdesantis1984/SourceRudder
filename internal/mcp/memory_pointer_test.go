package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/auth"
	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/connectors"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/memory"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

// TestStatefulSearchHandlerConsultsInjectedMemoryClient is the
// behavior-first RED gate for spec #4289 scenario "NewServer wires
// memory client": the *memory.Client passed at construction MUST
// be the same instance a stateful handler consults when it runs.
// The verify report #4338 flagged this scenario as UNTESTED because
// the prior smoke-only test only proved a non-nil server + non-empty
// registry; it never exercised the pointer through a stateful
// handler path.
//
// The proof is deterministic and behavior-first:
//
//   - Point the injected *memory.Client at an httptest stub URL.
//   - Drive a real search_web handler invocation end-to-end (which
//     goes through the MCP HandleToolsCall boundary, the stateful
//     makeSearchHandler closure, and the connector manager against
//     an httptest SearxNG stub).
//   - Assert the memory stub received exactly one POST whose body
//     carries the same query the operator asked for.
//
// If recordSearch stops consulting s.mem (regression), the memory
// stub sees zero hits and the test fails. If a future refactor
// substitutes a different *memory.Client (e.g. wraps it), the stub
// URL no longer matches and the assertion fires.
//
// RED proof (pre-fix): expected 1 memory POST after search_web, got 0.
// GREEN proof (post-fix): expected 1 memory POST after search_web, got 1.
func TestStatefulSearchHandlerConsultsInjectedMemoryClient(t *testing.T) {
	// Memory stub — counts POSTs and records the body so we can assert
	// the handler consulted the injected client with the right payload.
	var memHits int32
	var memLastBody []byte
	memStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&memHits, 1)
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		memLastBody = buf
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer memStub.Close()

	// SearxNG stub — answers search_web with a single valid result so
	// the handler reaches the post-success recordSearch branch.
	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[{"title":"t","url":"https://example.com/x","content":"c"}],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	// One *memory.Client pointer injected into NewServer. The httptest
	// URL is unique to this stub, so any POST that lands proves the
	// injected pointer is the one consulted.
	memClient := memory.NewClient(memStub.URL, "test-key")

	cacheSvc := cache.NewService(60)
	history := cache.NewHistoryService(10)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(searxng.URL, cacheSvc))

	srv := NewServer(
		cm, search.NewPlanner(),
		"stdio", ":8080", searxng.URL,
		60, 5000,
		fetch.NewFetcherService(5000),
		synthesis.NewService(),
		auth.NewValidator("k"),
		observability.New(),
		history, memClient,
	)

	// Drive the stateful handler through the real MCP boundary so the
	// handler-factory closure is exercised, not a direct field poke.
	params, _ := json.Marshal(map[string]interface{}{
		"name":      "search_web",
		"arguments": json.RawMessage(`{"query":"go fetch"}`),
	})
	if _, err := srv.HandleToolsCall(context.Background(), params); err != nil {
		t.Fatalf("HandleToolsCall search_web: %v", err)
	}

	// Assertion 1: exactly one POST landed on the injected memory
	// client. Zero means the handler did not consult s.mem (regression
	// or the wiring was never connected). More than one means the
	// client is being hit multiple times per search (over-firing).
	if got := atomic.LoadInt32(&memHits); got != 1 {
		t.Fatalf("expected exactly 1 POST against injected memory client after one search_web call, got %d (RED proof: handler does not consult *memory.Client)", got)
	}

	// Assertion 2: the body the handler sent carries the same query
	// the operator asked for. This pins the semantic: the injected
	// pointer was consulted with the right observation, not just any
	// pointer.
	var decoded map[string]interface{}
	if err := json.Unmarshal(memLastBody, &decoded); err != nil {
		t.Fatalf("expected memory POST body to be JSON, decode failed: %v (body=%q)", err, string(memLastBody))
	}
	if q, _ := decoded["query"].(string); q != "go fetch" {
		t.Fatalf("expected memory POST body.query=%q, got %#v", "go fetch", decoded["query"])
	}
}
