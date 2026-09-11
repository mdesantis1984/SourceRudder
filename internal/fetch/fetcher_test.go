package fetch

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// withSSRFDisabled swaps the package-level resolver for the duration
// of a test so httptest (which binds to 127.0.0.1) is reachable. It
// is restored when the test ends.
func withSSRFDisabled(t *testing.T) {
	t.Helper()
	prev := resolveAndValidateFn
	resolveAndValidateFn = func(host string) (net.IP, string, error) {
		return net.ParseIP("127.0.0.1"), host, nil
	}
	t.Cleanup(func() { resolveAndValidateFn = prev })
}

// TestFetcherAppliesConfiguredUserAgent is the Phase 6.1 RED gate.
// A configured User-Agent MUST reach the upstream across every fetch
// path; without it the response is rejected by Reddit, SearxNG, and
// most prod endpoints. The default UA is preserved by
// NewFetcherService(int) so callers without a config still get the
// prior production header.
func TestFetcherAppliesConfiguredUserAgent(t *testing.T) {
	withSSRFDisabled(t)
	const customUA = "sourcerudder/test-ua/42"

	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>UA test</title></head><body>x</body></html>"))
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{UserAgent: customUA, TimeoutMs: 5000})
	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	select {
	case ua := <-got:
		if ua != customUA {
			t.Fatalf("expected User-Agent %q, got %q", customUA, ua)
		}
	default:
		t.Fatal("upstream never recorded the User-Agent header")
	}
}

// TestFetcherOutcomeSuccessOn2xx is the Phase 6.5 RED gate. A 2xx
// response from the upstream MUST classify Outcome="success" with
// Status equal to the upstream code.
func TestFetcherOutcomeSuccessOn2xx(t *testing.T) {
	withSSRFDisabled(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000})
	resp, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if resp.Outcome != "success" {
		t.Fatalf("expected Outcome=success, got %q", resp.Outcome)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("expected Status=200, got %d", resp.Status)
	}
	if resp.Attempts != 1 {
		t.Fatalf("expected Attempts=1 on a single happy-path call, got %d", resp.Attempts)
	}
}

// TestFetcherBlockedTargetOnPrivateIP is the Phase 6.7 RED gate.
// The fetcher MUST refuse to dial a private/loopback IP. The spec
// classifies this as Outcome="blocked-target".
func TestFetcherBlockedTargetOnPrivateIP(t *testing.T) {
	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000})
	resp, err := f.Fetch(context.Background(), "http://127.0.0.1:8080/secret")
	if err == nil {
		t.Fatal("expected error from private-IP fetch")
	}
	if resp == nil {
		t.Fatal("expected populated FetchResponse with Outcome=blocked-target")
	}
	if resp.Outcome != "blocked-target" {
		t.Fatalf("expected Outcome=blocked-target, got %q", resp.Outcome)
	}
}

// TestFetcherNewDefaultPreservedForLegacyCaller locks in the spec
// contract: NewFetcherService(int) still returns a usable service
// with a non-empty User-Agent. Callers that never adopt the new
// Config path must not regress.
func TestFetcherNewDefaultPreservedForLegacyCaller(t *testing.T) {
	f := NewFetcherService(5000)
	if f == nil {
		t.Fatal("NewFetcherService returned nil")
	}
	if f.cfg.UserAgent == "" {
		t.Fatal("NewFetcherService must apply a non-empty default User-Agent")
	}
	if !strings.Contains(f.cfg.UserAgent, "SourceRudder") {
		t.Fatalf("default User-Agent must reference SourceRudder identity, got %q", f.cfg.UserAgent)
	}
}

// TestFetcherMaxRedirectsHonored is the Phase 6.7 RED gate for the
// redirect cap. The fetcher MUST follow at most MaxRedirects hops and
// classify overflow as Outcome="too-many-redirects".
func TestFetcherMaxRedirectsHonored(t *testing.T) {
	withSSRFDisabled(t)
	hops := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		if hops > 10 {
			t.Errorf("server saw %d hops; cap should have stopped earlier", hops)
		}
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{
		UserAgent:    "sourcerudder/test",
		TimeoutMs:    5000,
		MaxRedirects: 3,
	})
	resp, err := f.Fetch(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected redirect-cap error")
	}
	if resp == nil {
		t.Fatal("expected populated FetchResponse")
	}
	if resp.Outcome != "too-many-redirects" {
		t.Fatalf("expected Outcome=too-many-redirects, got %q", resp.Outcome)
	}
}

// TestFetcherRetryCancellable is the Phase 12.5 RED gate. When the
// caller cancels the context during the backoff between retries, the
// fetcher MUST return Outcome=timeout immediately instead of waiting
// out a non-cancellable time.Sleep. The previous shape uses a blank
// time.Sleep, so a SIGTERM during a 1.6s exponential backoff blocks
// the lifecycle for the full window.
func TestFetcherRetryCancellable(t *testing.T) {
	withSSRFDisabled(t)
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		// Always 503 so the fetcher retries, which triggers backoff.
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{
		UserAgent:    "sourcerudder/test",
		TimeoutMs:    5000,
		MaxAttempts:  4,
		BaseBackoff:  200 * time.Millisecond,
		MaxRedirects: 5,
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Give the first attempt time to land, then cancel while the
	// second backoff is in flight.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	resp, err := f.Fetch(ctx, srv.URL)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error after cancellation")
	}
	if resp.Outcome != "timeout" {
		t.Fatalf("expected Outcome=timeout, got %q (Warnings=%v)", resp.Outcome, resp.Warnings)
	}
	// Backoff to attempt 4 is ~1.6s with the deterministic jitter; a
	// real cancellation must short-circuit far below that.
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Fetch did not honor cancellation: took %v (backoff ignored ctx)", elapsed)
	}
	// The 503 retry path should have hit at least once but not all
	// four attempts.
	if attempts < 1 {
		t.Fatalf("server received no requests (attempts=%d)", attempts)
	}
}

// TestCheckLinkStatusCancellable is the Phase 12.5 RED gate for the
// rate-limiter path. The shared time.Ticker.Channel receive is not
// cancellable; cancelling the ctx between two consecutive requests
// MUST stop the goroutine from waiting indefinitely.
func TestCheckLinkStatusCancellable(t *testing.T) {
	withSSRFDisabled(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := NewFetcherServiceWithConfig(Config{
		UserAgent:    "sourcerudder/test",
		TimeoutMs:    5000,
		MaxAttempts:  1,
		BaseBackoff:  50 * time.Millisecond,
		MaxRedirects: 5,
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Pre-cancel so even the first receive from the rate limiter ticks
	// races against a cancelled context.
	cancel()

	urls := []string{srv.URL, srv.URL, srv.URL, srv.URL, srv.URL}
	start := time.Now()
	results, err := f.CheckLinkStatus(ctx, urls)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("CheckLinkStatus returned error: %v", err)
	}
	if len(results) != len(urls) {
		t.Fatalf("expected %d results, got %d", len(urls), len(results))
	}
	// The rate limiter ticks every 200ms; with 5 URLs and a cancelled
	// context, the batch must NOT wait for 5 ticks.
	if elapsed > 400*time.Millisecond {
		t.Fatalf("CheckLinkStatus honored the disabled rate-limiter queue: took %v", elapsed)
	}
}

func TestCheckLinkStatusBoundsBatchAndPreservesOrder(t *testing.T) {
	f := NewFetcherService(5000)
	if _, err := f.CheckLinkStatus(context.Background(), make([]string, 101)); err == nil {
		t.Fatal("expected oversized URL batch to be rejected")
	}

	urls := []string{"http://localhost/first", "ftp://example.com/second"}
	start := time.Now()
	results, err := f.CheckLinkStatus(context.Background(), urls)
	if err != nil {
		t.Fatalf("CheckLinkStatus: %v", err)
	}
	for i := range urls {
		if results[i]["url"] != urls[i] {
			t.Fatalf("result %d lost input order: %#v", i, results[i])
		}
	}
	if time.Since(start) < 2*rateLimiterInterval {
		t.Fatal("link checks were not globally paced")
	}
}

func TestFetcherPinsDialToApprovedIP(t *testing.T) {
	// Hook the resolver to map synthetic host -> 127.0.0.1.
	prev := resolveAndValidateFn
	resolveAndValidateFn = func(host string) (net.IP, string, error) {
		if host != "example.test" {
			t.Fatalf("resolver hook called for unexpected host %q; expected example.test", host)
		}
		return net.ParseIP("127.0.0.1"), "example.test", nil
	}
	t.Cleanup(func() { resolveAndValidateFn = prev })

	// Capture the dial IP from the request's RemoteAddr (set by the
	// listener) and the Host header on the upstream.
	var dialIP atomic.Value
	dialIP.Store(net.IP(nil))
	gotHost := make(chan string, 1)
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			dialIP.Store(net.ParseIP(host))
			gotHost <- r.Host
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}),
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("test listener: %v", err)
	}
	dialPort := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() { _ = srv.Close() })

	url := "http://example.test:" + strconv.Itoa(dialPort) + "/pin"
	f := NewFetcherServiceWithConfig(Config{UserAgent: "sourcerudder/test", TimeoutMs: 5000})
	resp, err := f.Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("Fetch: %v (Outcome=%s Warnings=%v)", err, resp.Outcome, resp.Warnings)
	}
	if resp.Outcome != "success" {
		t.Fatalf("expected Outcome=success, got %q", resp.Outcome)
	}

	// Host header on the upstream side. http.Request.Host carries
	// host:port for HTTP and just host for HTTPS, so the assertion
	// is "example.test" as the host portion.
	select {
	case h := <-gotHost:
		if !strings.HasPrefix(h, "example.test") {
			t.Fatalf("Host header: got %q, want prefix example.test", h)
		}
	default:
		t.Fatal("upstream handler never invoked")
	}

	got := dialIP.Load().(net.IP)
	if !got.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("dial IP: got %v, want 127.0.0.1 (resolveAndValidateFn was bypassed)", got)
	}
}
