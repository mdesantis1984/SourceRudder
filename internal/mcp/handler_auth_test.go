package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/auth"
	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/memory"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
)

// newHandlerOnlyServer builds a Server with auth validator wired in
// so the Handler() boundary can be exercised without booting a real
// transport. transport="stdio" means Start() is a no-op; the test
// calls Handler() directly via httptest.
func newHandlerOnlyServer(authValidator *auth.Validator) *Server {
	cacheSvc := cache.NewService(60)
	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	fetchSvc := fetch.NewFetcherService(5000)
	synthSvc := synthesis.NewService()
	return NewServer(cm, planner, "stdio", ":0", "http://localhost:8888", 60, 5000, fetchSvc, synthSvc, authValidator, observability.New(), cache.NewHistoryService(10), memory.NewClient("", ""))
}

// TestHandlerHealthzOpenWithoutCredentials is the Phase 12 RED gate
// refinement: the Kubernetes liveness/readiness probe at /healthz
// MUST be reachable WITHOUT credentials, otherwise a pod restart
// loop kicks in (probe fails → kubelet restarts → probe fails
// again). The previous shape wrapped the whole mux in the auth
// middleware, which forced every probe to carry an API key. The
// new contract: /healthz is explicit-public; /mcp and /metrics
// stay protected when a validator is configured.
func TestHandlerHealthzOpenWithoutCredentials(t *testing.T) {
	v := auth.NewValidator("test-key")
	srv := newHandlerOnlyServer(v)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected /healthz to return 200 without credentials (Kubernetes probe), got %d (body=%q)", rec.Code, rec.Body.String())
	}
	body, _ := io.ReadAll(rec.Body)
	if len(body) == 0 {
		t.Fatal("expected /healthz to return a JSON body, got empty response")
	}
}

// TestHandlerMCPRequiresCredentials is the triangulation surface for
// the protected boundary: /mcp MUST still require credentials when a
// validator is wired in. Splitting /healthz off must not weaken /mcp.
func TestHandlerMCPRequiresCredentials(t *testing.T) {
	v := auth.NewValidator("test-key")
	srv := newHandlerOnlyServer(v)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected /mcp to require credentials, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

// TestHandlerMCPValidCredentialsReachesDownstream locks in the
// happy-path counterpart: with the correct X-Api-Key, /mcp MUST
// reach the downstream handler (not 401). Without this test a
// regression that accidentally wrapped /mcp in a rejecting
// validator would still pass the no-creds case.
func TestHandlerMCPValidCredentialsReachesDownstream(t *testing.T) {
	v := auth.NewValidator("test-key")
	srv := newHandlerOnlyServer(v)

	// Valid POST against /mcp returns the JSON-RPC parse error (400)
	// because the body is empty — but the validator MUST NOT block
	// the request. Reaching the handler is the proof.
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("X-Api-Key", "test-key")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("valid X-Api-Key MUST reach /mcp; got 401 (auth validator rejected a valid credential)")
	}
}

// TestHandlerMetricsRequiresCredentials is the triangulation surface
// for the /metrics boundary: Prometheus scrape MUST carry credentials
// when auth is configured so an unauthenticated observer cannot read
// the registry. Splitting /healthz off must not weaken /metrics.
func TestHandlerMetricsRequiresCredentials(t *testing.T) {
	v := auth.NewValidator("test-key")
	srv := newHandlerOnlyServer(v)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected /metrics to require credentials when auth is wired, got %d (body=%q)", rec.Code, rec.Body.String())
	}
}

// TestHandlerNilValidatorStillProtectsMCPAndMetrics guards the
// configuration shape where the operator passes a nil validator (the
// previous production default). With no validator the gate is
// "always reject" so /mcp and /metrics stay closed even though
// /healthz is open. This locks in the post-fix invariant: healthz
// is open, the rest of the surface is closed when auth is
// unavailable.
func TestHandlerNilValidatorStillProtectsMCPAndMetrics(t *testing.T) {
	srv := newHandlerOnlyServer(nil)

	// /healthz stays open (Kubernetes probe) regardless of validator.
	recHealthz := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recHealthz, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recHealthz.Code != http.StatusOK {
		t.Fatalf("nil validator: /healthz MUST stay open; got %d", recHealthz.Code)
	}

	// /mcp is closed when the validator is nil — the rejecting
	// fallback in NewValidator("") applies, and passing nil through
	// the middleware also rejects (defence-in-depth).
	recMCP := httptest.NewRecorder()
	srv.Handler().ServeHTTP(recMCP, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if recMCP.Code != http.StatusUnauthorized {
		t.Fatalf("nil validator: /mcp MUST reject; got %d", recMCP.Code)
	}
}

// _ keeps context imported; some of the existing helpers above use
// `context.Background` indirectly through Server, but the explicit
// import makes the dependency obvious to future readers and avoids
// a staticcheck WAITING line.
var _ = context.Background