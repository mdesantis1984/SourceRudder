package fetch

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// TestFetcherTimeoutEnforcedFromConfig is the Phase 6 / Spec
// "Configurable timeout" RED gate. A 50ms timeout MUST abort the
// fetch before the slow upstream can respond. The previous shape
// used http.Client.Timeout without a per-request deadline, so a
// hung upstream kept the lifecycle open until the operator killed
// the process. The test pins MaxAttempts=1 so the timeout is
// classified as Outcome=timeout (a single attempt cannot retry
// itself; retry exhaustion is covered by TestFetcherRetryExhaustionClassified).
func TestFetcherTimeoutEnforcedFromConfig(t *testing.T) {
	withSSRFDisabled(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sleep longer than the configured timeout so the client
		// MUST observe a timeout, not a response.
		select {
		case <-time.After(2 * time.Second):
			w.WriteHeader(http.StatusOK)
		case <-r.Context().Done():
			return
		}
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{
		UserAgent:   "sourcerudder/test",
		TimeoutMs:   50, // 50 ms; srv will sleep 2 s
		MaxAttempts: 1,  // single attempt: classify as timeout, not retry-exhausted
	})
	start := time.Now()
	resp, err := f.Fetch(context.Background(), srv.URL)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected timeout error, got nil (resp=%+v)", resp)
	}
	if resp == nil {
		t.Fatal("expected populated FetchResponse with Outcome=timeout, got nil")
	}
	if resp.Outcome != OutcomeTimeout {
		t.Fatalf("expected Outcome=timeout, got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took %v; expected < 500ms (configured 50ms)", elapsed)
	}
}

// TestFetcherSafeRedirectHonored is the spec's "Redirect safety and
// SSRF re-validation" GREEN-path RED gate. A 2-hop redirect chain
// that resolves to a 200 OK MUST classify Outcome=success and
// capture the full chain on the response so an MCP client can see
// the path the request took.
func TestFetcherSafeRedirectHonored(t *testing.T) {
	withSSRFDisabled(t)
	hops := atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hops.Add(1)
		switch r.URL.Path {
		case "/start":
			http.Redirect(w, r, "/mid", http.StatusFound)
		case "/mid":
			http.Redirect(w, r, "/final", http.StatusFound)
		case "/final":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html><head><title>redirected</title></head><body>final</body></html>"))
		default:
			http.NotFound(w, r)
		}
		_ = n
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000, MaxRedirects: 5})
	resp, err := f.Fetch(context.Background(), srv.URL+"/start")
	if err != nil {
		t.Fatalf("Fetch: %v (Outcome=%s)", err, resp.Outcome)
	}
	if resp.Outcome != OutcomeSuccess {
		t.Fatalf("expected Outcome=success after a 2-hop redirect, got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected Status=200 after redirect, got %d", resp.Status)
	}
	if len(resp.RedirectChain) != 2 {
		t.Fatalf("expected 2 entries in RedirectChain, got %d (%v)", len(resp.RedirectChain), resp.RedirectChain)
	}
}

// TestFetcherBlockedRedirectClassifiedCorrectly is the
// triangulation surface for the spec's "Redirect safety and SSRF
// re-validation" requirement. The manual loop MUST classify a
// redirect that points at a non-public IP as
// Outcome=blocked-target — NOT silent success. The first hop is
// reached via a custom resolver hook that pins the dial to
// 127.0.0.1 (so httptest can serve it); the redirect target
// `localhost` is rejected by the same hook so the fetcher MUST
// surface the classification instead of attempting a dial.
func TestFetcherBlockedRedirectClassifiedCorrectly(t *testing.T) {
	prev := resolveAndValidateFn
	resolveAndValidateFn = func(host string) (net.IP, string, error) {
		if host == "localhost" {
			return nil, "", errors.New("blocked target: non-public address in resolved set (test)")
		}
		return net.ParseIP("127.0.0.1"), host, nil
	}
	t.Cleanup(func() { resolveAndValidateFn = prev })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:1/secret", http.StatusFound)
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000, MaxRedirects: 5})
	resp, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatalf("expected blocked-target error on unsafe redirect, got nil (resp=%+v)", resp)
	}
	if resp == nil {
		t.Fatal("expected populated FetchResponse")
	}
	if resp.Outcome != OutcomeBlockedTarget {
		t.Fatalf("expected Outcome=blocked-target (the redirect to localhost is blocked by the resolver guard), got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
}

// TestFetcherHTTP4xxClassifiedAsHttpError locks in the spec's
// "Explicit outcomes and status" requirement for the 4xx class.
// A 404 MUST classify Outcome=http-error with Status=404 so an
// MCP client can distinguish a missing page from a transport
// failure.
func TestFetcherHTTP4xxClassifiedAsHttpError(t *testing.T) {
	withSSRFDisabled(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not here", http.StatusNotFound)
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000, MaxAttempts: 1, MaxRedirects: 5})
	resp, err := f.Fetch(context.Background(), srv.URL+"/missing")
	if err == nil {
		t.Fatalf("expected http-error for 404, got nil (resp=%+v)", resp)
	}
	if resp.Outcome != OutcomeHTTPError {
		t.Fatalf("expected Outcome=http-error, got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
	if resp.Status != http.StatusNotFound {
		t.Fatalf("expected Status=404, got %d", resp.Status)
	}
}

// TestFetcherRetryOn429 is the spec's "Transient-only retries"
// RED gate for 429. A persistent 429 MUST trigger retry up to
// MaxAttempts and finally classify
// Outcome=transient-failure-retried-exhausted.
func TestFetcherRetryOn429(t *testing.T) {
	withSSRFDisabled(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{
		UserAgent:    "sourcerudder/test",
		TimeoutMs:    5000,
		MaxAttempts:  3,
		BaseBackoff:  20 * time.Millisecond,
		MaxRedirects: 5,
	})
	resp, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatalf("expected exhausted-retry error, got nil (resp=%+v)", resp)
	}
	if resp.Outcome != OutcomeTransientFailureRetriedExhausted {
		t.Fatalf("expected Outcome=transient-failure-retried-exhausted, got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
	if got := hits.Load(); got < 3 {
		t.Fatalf("expected fetcher to retry at least 3 times on 429, got %d hits", got)
	}
	if resp.Attempts != 3 {
		t.Fatalf("expected Attempts=3, got %d", resp.Attempts)
	}
}

// TestFetcherRetryOn502 then 200 is the spec's GREEN-path
// counterpart for transient 5xx. The fetcher MUST retry a 502 and
// succeed when the upstream comes back; Outcome=success with
// Attempts=2 (initial + 1 retry) proves the retry logic actually
// ran rather than swallowed the retry contract.
func TestFetcherRetryOn502(t *testing.T) {
	withSSRFDisabled(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>recovered</title></head><body>ok</body></html>"))
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{
		UserAgent:    "sourcerudder/test",
		TimeoutMs:    5000,
		MaxAttempts:  3,
		BaseBackoff:  20 * time.Millisecond,
		MaxRedirects: 5,
	})
	resp, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v (Outcome=%s)", err, resp.Outcome)
	}
	if resp.Outcome != OutcomeSuccess {
		t.Fatalf("expected Outcome=success after retry, got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
	if resp.Attempts != 2 {
		t.Fatalf("expected Attempts=2 (initial 502 + retry), got %d", resp.Attempts)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("expected 2 hits (502 then 200), got %d", got)
	}
}

// TestFetcherRetryOn503Then200 mirrors TestFetcherRetryOn502 with
// the 503 status. The retry contract covers 502-504, so 503 must
// also retry-and-recover. Triangulating against the 502 case
// forces the retry classifier to actually inspect the status
// range rather than hardcoding one value.
func TestFetcherRetryOn503Then200(t *testing.T) {
	withSSRFDisabled(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>503-recovered</title></head><body>ok</body></html>"))
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{
		UserAgent:    "sourcerudder/test",
		TimeoutMs:    5000,
		MaxAttempts:  3,
		BaseBackoff:  20 * time.Millisecond,
		MaxRedirects: 5,
	})
	resp, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v (Outcome=%s)", err, resp.Outcome)
	}
	if resp.Outcome != OutcomeSuccess {
		t.Fatalf("expected Outcome=success after retry, got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
	if resp.Attempts != 2 {
		t.Fatalf("expected Attempts=2 (initial 503 + retry), got %d", resp.Attempts)
	}
}

// TestFetcherRetryExhaustionClassified exercises the
// transient-failure-retried-exhausted path with MaxAttempts=2 and
// a persistent 503. The fetcher MUST report Attempts=MaxAttempts
// and Outcome=transient-failure-retried-exhausted so the MCP
// client can switch on the outcome instead of inspecting the Go
// error string.
func TestFetcherRetryExhaustionClassified(t *testing.T) {
	withSSRFDisabled(t)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{
		UserAgent:    "sourcerudder/test",
		TimeoutMs:    5000,
		MaxAttempts:  2,
		BaseBackoff:  20 * time.Millisecond,
		MaxRedirects: 5,
	})
	resp, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatalf("expected exhaustion error, got nil (resp=%+v)", resp)
	}
	if resp.Outcome != OutcomeTransientFailureRetriedExhausted {
		t.Fatalf("expected Outcome=transient-failure-retried-exhausted, got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
	if resp.Attempts != 2 {
		t.Fatalf("expected Attempts=2, got %d", resp.Attempts)
	}
	if got := hits.Load(); got != 2 {
		t.Fatalf("expected 2 hits (MaxAttempts=2), got %d", got)
	}
}

// TestFetchResponseWireContractRoundTrip is the FetchResponse JSON
// wire-contract RED gate. The previous FetchResponse_test only
// constructed a value and asserted addressability; this test
// actually marshals the response, unmarshals it back, and asserts
// every wire field survives the round-trip. Without this test a
// future tag change could silently break the MCP wire contract.
func TestFetchResponseWireContractRoundTrip(t *testing.T) {
	original := types.FetchResponse{
		URL:           "https://example.com/page",
		Title:         "Example Domain",
		Content:       "<html><body>x</body></html>",
		Outcome:       OutcomeSuccess,
		Status:        200,
		RedirectChain: []string{"https://example.com/old", "https://example.com/mid"},
		Attempts:      2,
		Warnings:      []string{"rate-limited"},
		Metadata:      map[string]string{"author": "alice"},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded types.FetchResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.URL != original.URL {
		t.Fatalf("URL: got %q, want %q", decoded.URL, original.URL)
	}
	if decoded.Outcome != original.Outcome {
		t.Fatalf("Outcome: got %q, want %q", decoded.Outcome, original.Outcome)
	}
	if decoded.Status != original.Status {
		t.Fatalf("Status: got %d, want %d", decoded.Status, original.Status)
	}
	if decoded.Attempts != original.Attempts {
		t.Fatalf("Attempts: got %d, want %d", decoded.Attempts, original.Attempts)
	}
	if len(decoded.RedirectChain) != len(original.RedirectChain) {
		t.Fatalf("RedirectChain length: got %d, want %d", len(decoded.RedirectChain), len(original.RedirectChain))
	}
	for i, hop := range original.RedirectChain {
		if decoded.RedirectChain[i] != hop {
			t.Fatalf("RedirectChain[%d]: got %q, want %q", i, decoded.RedirectChain[i], hop)
		}
	}
	if decoded.Metadata["author"] != "alice" {
		t.Fatalf("Metadata round-trip: got %v, want author=alice", decoded.Metadata)
	}
}

// TestFetchResponseHandlerSurfaceExercisesWireContract is the
// FetchResponse "real handler" RED gate. The MCP handler MUST
// surface the live FetchResponse.Outcome on the wire — agents
// pattern-match on the JSON field, not on Go errors. The test
// drives FetchAndExtract with mode="raw" (which always preserves
// the source HTML so the Content field is non-empty on the wire)
// and asserts every contract field reflects the real fetch
// result, not a struct-construction shortcut.
func TestFetchResponseHandlerSurfaceExercisesWireContract(t *testing.T) {
	withSSRFDisabled(t)
	const body = "<html><head><title>handler-surface</title></head><body><p>real content paragraph</p></body></html>"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000})
	resp, err := f.FetchAndExtract(context.Background(), srv.URL, "raw")
	if err != nil {
		t.Fatalf("FetchAndExtract: %v", err)
	}

	// Marshal-then-unmarshal the response to mirror the wire path.
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("unmarshal wire: %v", err)
	}

	// Required contract fields on the wire.
	if wire["url"] != srv.URL {
		t.Fatalf("wire.url: got %v, want %q", wire["url"], srv.URL)
	}
	if wire["outcome"] != OutcomeSuccess {
		t.Fatalf("wire.outcome: got %v, want %q", wire["outcome"], OutcomeSuccess)
	}
	if statusF, ok := wire["status"].(float64); !ok || int(statusF) != http.StatusOK {
		t.Fatalf("wire.status: got %v (type %T), want %d", wire["status"], wire["status"], http.StatusOK)
	}
	if _, ok := wire["title"]; !ok {
		t.Fatalf("wire.title missing on the response; got keys=%v", keysOf(wire))
	}
	if wireContent, ok := wire["content"].(string); !ok || wireContent == "" {
		t.Fatalf("wire.content missing or empty on the response; got %v (keys=%v)", wire["content"], keysOf(wire))
	}
	if wireMetadata, ok := wire["metadata"].(map[string]interface{}); !ok || len(wireMetadata) == 0 {
		t.Fatalf("wire.metadata missing or empty on the response; got %v (keys=%v)", wire["metadata"], keysOf(wire))
	}
}

// TestFetchResponseBlockedTargetWireContract locks in the
// non-success wire shape: a blocked target still returns a populated
// FetchResponse (not nil) with Outcome=blocked-target on the wire,
// and the same response object carries a non-nil error so the
// MCP wrapper can wrap it as a JSON-RPC error.
func TestFetchResponseBlockedTargetWireContract(t *testing.T) {
	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000})
	resp, err := f.Fetch(context.Background(), "http://127.0.0.1:1/secret")
	if err == nil {
		t.Fatal("expected error for blocked target")
	}
	if resp == nil {
		t.Fatal("expected populated FetchResponse even on blocked target (the MCP handler must wrap it, not nil-deref)")
	}
	if resp.Outcome != OutcomeBlockedTarget {
		t.Fatalf("Outcome: got %q, want %q", resp.Outcome, OutcomeBlockedTarget)
	}
	// Blocked-target must propagate the error reason into the
	// response Warnings slice. An MCP client receives the
	// FetchResponse and the JSON-RPC error side by side; if the
	// response Warnings are empty the operator cannot tell WHY the
	// target was blocked (Outcome alone says "blocked-target" but
	// not whether it was the URL text guard or the SSRF guard).
	// This is real behavior coverage: a future change that drops
	// `fr.Warnings = []string{err.Error()}` in fetcher.go would
	// make this test fail.
	if len(resp.Warnings) == 0 {
		t.Fatal("expected Warnings to describe blocked reason; got 0 entries")
	}
	if !strings.Contains(resp.Warnings[0], "non-public") {
		t.Fatalf("expected first Warning to mention 'non-public' (the SSRF guard reason); got %q", resp.Warnings[0])
	}
	if !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("expected error message to mention 'non-public' (the SSRF guard reason); got %q", err.Error())
	}
	raw, _ := json.Marshal(resp)
	if !contains(string(raw), `"outcome":"blocked-target"`) {
		t.Fatalf("wire JSON must contain outcome=blocked-target; got %s", raw)
	}
}

// TestFetchResponseBlockedTargetURLTextPropagatesWarnings is the
// triangulation surface for TestFetchResponseBlockedTargetWireContract.
// The IP-based path (`127.0.0.1`) goes through the SSRF resolver
// guard and produces a "non-public address" warning. The URL-text
// path (e.g. `http://localhost/secret`) goes through the
// validateURLText layer and produces a different reason —
// "internal host" / "localhost". Both paths MUST honor the same
// contract: a populated response with Outcome=blocked-target AND
// a non-empty Warnings slice carrying the reason. Without this
// test a regression in the URL-text branch (e.g. silently
// returning err before setting fr.Warnings) would slip past the
// IP-based test alone.
func TestFetchResponseBlockedTargetURLTextPropagatesWarnings(t *testing.T) {
	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000})
	resp, err := f.Fetch(context.Background(), "http://localhost/secret")
	if err == nil {
		t.Fatal("expected error for URL-text-blocked target")
	}
	if resp == nil {
		t.Fatal("expected populated FetchResponse even on URL-text block")
	}
	if resp.Outcome != OutcomeBlockedTarget {
		t.Fatalf("Outcome: got %q, want %q", resp.Outcome, OutcomeBlockedTarget)
	}
	// URL-text guard produces "internal host" / "localhost" reasons;
	// the IP-guard produces "non-public". Both are acceptable here
	// because we only assert the contract: Warnings non-empty and
	// carrying a reason that the operator can read.
	if len(resp.Warnings) == 0 {
		t.Fatal("expected Warnings to describe URL-text-block reason; got 0 entries")
	}
	if !strings.Contains(resp.Warnings[0], "internal host") && !strings.Contains(resp.Warnings[0], "localhost") {
		t.Fatalf("expected first Warning to mention URL-text-block reason; got %q", resp.Warnings[0])
	}
	raw, _ := json.Marshal(resp)
	if !contains(string(raw), `"outcome":"blocked-target"`) {
		t.Fatalf("wire JSON must contain outcome=blocked-target; got %s", raw)
	}
}

func TestFetcherRejectsOversizedResponse(t *testing.T) {
	withSSRFDisabled(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxFetchResponseBytes+1)))
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{TimeoutMs: 5000, MaxAttempts: 1})
	resp, err := f.Fetch(context.Background(), srv.URL)
	if err == nil || resp.Outcome != OutcomeNonTransientFailure {
		t.Fatalf("expected bounded non-transient failure, got response=%+v err=%v", resp, err)
	}
	if resp.Status != http.StatusOK || resp.Content != "" {
		t.Fatalf("oversized response leaked content or status: %+v", resp)
	}
}

// contains is a tiny helper to keep the wire-contract assertion
// self-contained.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func keysOf(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// keep the imports honest when the file shrinks; staticcheck
// otherwise flags the unused ones on every test deletion.
var _ = net.IPv4len
