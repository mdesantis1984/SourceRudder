package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// TestSearxNGConnectorsEmitDegradationMetric covers item 4: every
// SearxNG-backed connector that sees an unresponsive engine response
// must increment the sourcerudder_search_degraded_total counter with the
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
			before := metricValue(t, met.Handler(), c.name, "unresponsive_engines")
			_, err := c.ctor("metric-degraded-" + c.name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			after := metricValue(t, met.Handler(), c.name, "unresponsive_engines")
			if delta := after - before; delta != 1 {
				t.Errorf("expected %s degradation metric delta=1, got %d (before=%d after=%d)", c.name, delta, before, after)
			}
		})
	}
}

func TestRedditEmitDegradationMetric_SearxngForbidden(t *testing.T) {
	met := observability.New()
	observability.SetDefault(met)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	c := NewRedditConnector(RedditConfig{SearxngURL: srv.URL}, cache.NewService(60))
	_, err := c.Search(context.Background(), &types.SearchRequest{Query: "metric-reddit-searxng-forbidden"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metricValue(t, met.Handler(), "reddit", "searxng"); got != 0 {
		t.Errorf("HTTP failures use recordSearxngError and must not add a duplicate reddit/searxng metric, got %d", got)
	}
}

func TestRedditEmitDegradationMetric_SearxngRateLimited(t *testing.T) {
	met := observability.New()
	observability.SetDefault(met)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c := NewRedditConnector(RedditConfig{SearxngURL: srv.URL}, cache.NewService(60))
	_, err := c.Search(context.Background(), &types.SearchRequest{Query: "metric-reddit-429"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := metricValue(t, met.Handler(), "reddit", "searxng"); got != 0 {
		t.Errorf("HTTP failures use recordSearxngError and must not add a duplicate reddit/searxng metric, got %d", got)
	}
}

func TestRedditSearxngConfigSeamIsExposed(t *testing.T) {
	cfg := RedditConfig{
		SearxngURL: "https://example.test",
	}
	c := NewRedditConnector(cfg, cache.NewService(60))
	if c.cfg.SearxngURL != cfg.SearxngURL {
		t.Errorf("SearXNG URL was not retained on the connector: want %q got %q", cfg.SearxngURL, c.cfg.SearxngURL)
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
		"sourcerudder_search_degraded_total{source=\"" + source + "\",kind=\"" + kind + "\"}",
		"sourcerudder_search_degraded_total{kind=\"" + kind + "\",source=\"" + source + "\"}",
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
