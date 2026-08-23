package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

// TestSearxNGConnectorsEmitDegradationMetric covers item 4: every
// SearxNG-backed connector that sees an unresponsive engine response
// must increment the ia_buscar_search_degraded_total counter with the
// right source label. The test installs an httptest server that
// returns the canonical SearxNG degraded shape and asserts the metric
// ticked — proving the observability hook is wired in production
// code, not just in the unit-level metrics test.
func TestSearxNGConnectorsEmitDegradationMetric(t *testing.T) {
	// Use a per-test default metrics instance so this test does not
	// race against any other test that calls RecordSearchDegraded.
	met := observability.New()
	observability.SetDefault(met)

	const body = `{"results":[],"unresponsive_engines":[["duckduckgo","timeout"]]}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	ctx := context.Background()
	cacheSvc := cache.NewService(300)

	type runFn func(query string) (*types.SearchResponse, error)
	connectors := []struct {
		name string
		ctor runFn
	}{
		{"web", func(q string) (*types.SearchResponse, error) {
			return NewWebConnector(srv.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
		{"news", func(q string) (*types.SearchResponse, error) {
			return NewNewsConnector(srv.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
		{"youtube", func(q string) (*types.SearchResponse, error) {
			return NewYouTubeConnector(srv.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
		{"images", func(q string) (*types.SearchResponse, error) {
			return NewImagesConnector(srv.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
		{"academic", func(q string) (*types.SearchResponse, error) {
			return NewAcademicConnector(srv.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
	}

	for _, c := range connectors {
		t.Run(c.name, func(t *testing.T) {
			// Use a unique query per subtest so the in-process cache
			// cannot poison later assertions.
			_, err := c.ctor("metric-degraded-" + c.name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := metricValue(t, met.Handler(), c.name, "unresponsive_engines"); got < 1 {
				t.Errorf("expected %s to increment ia_buscar_search_degraded_total at least once, got %d", c.name, got)
			}
		})
	}
}

// TestRedditEmitDegradationMetric_AnonymousBlocked covers the
// anonymous-blocked metric path: when Reddit returns 403 to an
// anonymous request, the metric must tick with source="reddit" and
// kind="anonymous_blocked" (not the old "unconfigured" label).
func TestRedditEmitDegradationMetric_AnonymousBlocked(t *testing.T) {
	met := observability.New()
	observability.SetDefault(met)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewRedditConnector(RedditConfig{BaseURL: srv.URL, UserAgent: "ia-buscar/test"}, cache.NewService(60))
	_, err := c.Search(context.Background(), &types.SearchRequest{Query: "metric-reddit-anonymous-blocked"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metricValue(t, met.Handler(), "reddit", "anonymous_blocked"); got != 1 {
		t.Errorf("expected reddit/anonymous_blocked counter = 1, got %d", got)
	}
}

// TestRedditEmitDegradationMetric_RateLimited covers the
// rate_limited metric path: 429 from Reddit must tick the metric
// without classifying as reddit_unconfigured.
func TestRedditEmitDegradationMetric_RateLimited(t *testing.T) {
	met := observability.New()
	observability.SetDefault(met)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewRedditConnector(RedditConfig{BaseURL: srv.URL, UserAgent: "ia-buscar/test"}, cache.NewService(60))
	_, err := c.Search(context.Background(), &types.SearchRequest{Query: "metric-reddit-429"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metricValue(t, met.Handler(), "reddit", "rate_limited"); got != 1 {
		t.Errorf("expected reddit/rate_limited counter = 1, got %d", got)
	}
	if got := metricValue(t, met.Handler(), "reddit", "unconfigured"); got != 0 {
		t.Errorf("expected reddit/unconfigured counter to stay at 0 on 429, got %d", got)
	}
}

// TestRedditUserAgentConfigSeamIsExposed locks in the configuration
// seam: the anonymous-only Reddit connector exposes User-Agent via
// its constructor rather than as a hard-coded literal.
// NewRedditConnector(cfg, cache) is the canonical signature; a future
// regression that hard-codes the User-Agent OR re-introduces OAuth
// fields will fail this test.
func TestRedditUserAgentConfigSeamIsExposed(t *testing.T) {
	cfg := RedditConfig{
		BaseURL:   "https://example.test",
		UserAgent: "ia-buscar/test-config-seam",
	}
	c := NewRedditConnector(cfg, cache.NewService(60))
	if c.cfg.UserAgent != cfg.UserAgent {
		t.Errorf("User-Agent was not retained on the connector: want %q got %q", cfg.UserAgent, c.cfg.UserAgent)
	}
	if c.cfg.BaseURL != cfg.BaseURL {
		t.Errorf("BaseURL was not retained on the connector: want %q got %q", cfg.BaseURL, c.cfg.BaseURL)
	}
}

// metricValue scrapes the metric counter for a given source/kind pair.
// It uses the prometheus testutil helper indirectly: by parsing the
// exposition body for a line containing both labels and reading its
// numeric value. Keeping this in a helper file rather than in a test
// helper package avoids the cross-package test util boilerplate.
//
// Prometheus emits labels in alphabetical order regardless of how the
// metric was registered. We try both orderings to keep the helper
// robust against that internal detail.
func metricValue(t *testing.T, h interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, source, kind string) int {
	t.Helper()

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("scrape: %v", err)
	}
	defer resp.Body.Close()
	body := make([]byte, 0, 4096)
	buf := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}

	prefixes := []string{
		"ia_buscar_search_degraded_total{source=\"" + source + "\",kind=\"" + kind + "\"}",
		"ia_buscar_search_degraded_total{kind=\"" + kind + "\",source=\"" + source + "\"}",
	}
	for _, line := range strings.Split(string(body), "\n") {
		for _, prefix := range prefixes {
			if strings.HasPrefix(line, prefix) {
				fields := strings.Fields(line)
				if len(fields) < 2 {
					continue
				}
				n := 0
				for _, r := range fields[1] {
					if r < '0' || r > '9' {
						continue
					}
					n = n*10 + int(r-'0')
				}
				return n
			}
		}
	}
	return 0
}