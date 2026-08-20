package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

// TestSearxNGConnectorDegradation asserts that every SearxNG-backed connector
// surfaces upstream degradation: HTTP >=400 returns Partial=true with a warning
// containing the status code, and a 200 OK body with non-empty
// unresponsive_engines plus empty results also returns Partial=true with a
// warning naming at least one failing engine.
func TestSearxNGConnectorDegradation(t *testing.T) {
	ctx := context.Background()
	cacheSvc := cache.NewService(300)

	scenarios := []struct {
		name        string
		body        string
		status      int
		mustContain string
	}{
		{"http_503", "", http.StatusServiceUnavailable, "503"},
		{"unresponsive_engines", `{"results":[],"unresponsive_engines":[["duckduckgo","timeout"]]}`, http.StatusOK, "duckduckgo"},
	}

	type runFn func(searxngURL string) func(query string) (*types.SearchResponse, error)

	connectors := []struct {
		name string
		ctor runFn
	}{
		{"web", func(u string) func(string) (*types.SearchResponse, error) {
			c := NewWebConnector(u, cacheSvc)
			return func(q string) (*types.SearchResponse, error) {
				return c.Search(ctx, &types.SearchRequest{Query: q})
			}
		}},
		{"news", func(u string) func(string) (*types.SearchResponse, error) {
			c := NewNewsConnector(u, cacheSvc)
			return func(q string) (*types.SearchResponse, error) {
				return c.Search(ctx, &types.SearchRequest{Query: q})
			}
		}},
		{"youtube", func(u string) func(string) (*types.SearchResponse, error) {
			c := NewYouTubeConnector(u, cacheSvc)
			return func(q string) (*types.SearchResponse, error) {
				return c.Search(ctx, &types.SearchRequest{Query: q})
			}
		}},
		{"images", func(u string) func(string) (*types.SearchResponse, error) {
			c := NewImagesConnector(u, cacheSvc)
			return func(q string) (*types.SearchResponse, error) {
				return c.Search(ctx, &types.SearchRequest{Query: q})
			}
		}},
		{"academic", func(u string) func(string) (*types.SearchResponse, error) {
			c := NewAcademicConnector(u, cacheSvc)
			return func(q string) (*types.SearchResponse, error) {
				return c.Search(ctx, &types.SearchRequest{Query: q})
			}
		}},
	}

	for _, conn := range connectors {
		for _, sc := range scenarios {
			t.Run(conn.name+"/"+sc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if sc.body != "" {
						w.Header().Set("Content-Type", "application/json")
					}
					w.WriteHeader(sc.status)
					if sc.body != "" {
						_, _ = w.Write([]byte(sc.body))
					}
				}))
				defer srv.Close()

				resp, err := conn.ctor(srv.URL)(conn.name + "-" + sc.name)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !resp.Partial {
					t.Errorf("expected Partial=true for %s/%s, got false (warnings=%v)", conn.name, sc.name, resp.Warnings)
				}
				if len(resp.Warnings) == 0 {
					t.Fatalf("expected non-empty warnings for %s/%s", conn.name, sc.name)
				}
				if !strings.Contains(strings.Join(resp.Warnings, " | "), sc.mustContain) {
					t.Errorf("expected warnings to contain %q for %s/%s, got %v", sc.mustContain, conn.name, sc.name, resp.Warnings)
				}
			})
		}
	}
}

// TestRedditConnectorDegradation asserts that Reddit surfaces upstream
// degradation: HTTP >=400 returns Partial=true with the status code in the
// warning, and a transport-level failure returns Partial=true with a warning
// explicitly tagged "transport".
func TestRedditConnectorDegradation(t *testing.T) {
	ctx := context.Background()
	cacheSvc := cache.NewService(300)

	t.Run("http_429", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		c := NewRedditConnector(RedditConfig{BaseURL: srv.URL}, cacheSvc)
		c.client = &http.Client{Timeout: 5 * time.Second}

		resp, err := c.Search(ctx, &types.SearchRequest{Query: "go-429"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resp.Partial {
			t.Errorf("expected Partial=true for reddit 429, got false (warnings=%v)", resp.Warnings)
		}
		if !strings.Contains(strings.Join(resp.Warnings, " | "), "429") {
			t.Errorf("expected warnings to contain %q for reddit 429, got %v", "429", resp.Warnings)
		}
	})

	t.Run("transport_failure", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c := NewRedditConnector(RedditConfig{BaseURL: srv.URL}, cacheSvc)
		c.client = &http.Client{Timeout: time.Nanosecond}

		resp, err := c.Search(ctx, &types.SearchRequest{Query: "go-transport"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !resp.Partial {
			t.Errorf("expected Partial=true for reddit transport failure, got false (warnings=%v)", resp.Warnings)
		}
		if !strings.Contains(strings.ToLower(strings.Join(resp.Warnings, " | ")), "transport") {
			t.Errorf("expected warnings to mention transport for reddit transport failure, got %v", resp.Warnings)
		}
	})

	t.Run("http_403_without_oauth", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		c := NewRedditConnector(RedditConfig{BaseURL: srv.URL}, cacheSvc)
		c.client = &http.Client{Timeout: 5 * time.Second}

		resp, err := c.Search(ctx, &types.SearchRequest{Query: "go-403"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// The 403 without OAuth is a configuration problem, not an
		// upstream degradation, so it must surface as
		// Strategy="reddit_unconfigured" with an actionable warning,
		// not as Partial=true.
		if resp.Strategy != "reddit_unconfigured" {
			t.Errorf("expected Strategy=reddit_unconfigured, got %q", resp.Strategy)
		}
		if resp.Partial {
			t.Errorf("expected Partial=false for unconfigured reddit, got true (warnings=%v)", resp.Warnings)
		}
		if len(resp.Warnings) == 0 || !strings.Contains(strings.ToLower(resp.Warnings[0]), "reddit_unconfigured") {
			t.Errorf("expected warnings to start with reddit_unconfigured, got %v", resp.Warnings)
		}
		if len(resp.Results) != 0 {
			t.Errorf("expected empty results for unconfigured reddit, got %d", len(resp.Results))
		}
	})

	t.Run("http_403_with_oauth", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}))
		defer srv.Close()

		c := NewRedditConnector(RedditConfig{
			BaseURL:      srv.URL,
			ClientID:     "configured",
			ClientSecret: "configured",
		}, cacheSvc)
		c.client = &http.Client{Timeout: 5 * time.Second}

		resp, err := c.Search(ctx, &types.SearchRequest{Query: "go-403-oauth"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// OAuth is configured but Reddit still rejected the request:
		// this is upstream degradation, not a config problem.
		if resp.Strategy == "reddit_unconfigured" {
			t.Errorf("expected strategy NOT to be reddit_unconfigured when OAuth is set, got %q", resp.Strategy)
		}
		if !resp.Partial {
			t.Errorf("expected Partial=true for reddit 403 with OAuth, got false")
		}
	})
}
