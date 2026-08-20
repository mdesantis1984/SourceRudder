package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

// TestHealthyEmptyPreservation locks in the contract that a healthy 200 OK
// SearxNG response (empty results, with or without an empty
// unresponsive_engines array) and a healthy 200 OK Reddit response (empty
// children) MUST NOT be reported as partial and MUST NOT emit warnings. It
// guards every connector from accidentally treating a genuine empty result as
// upstream degradation.
func TestHealthyEmptyPreservation(t *testing.T) {
	ctx := context.Background()
	cacheSvc := cache.NewService(300)

	searxng := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
	}))
	defer searxng.Close()

	reddit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"children":[]}}`))
	}))
	defer reddit.Close()

	searxngConnectors := []struct {
		name string
		run  func(query string) (*types.SearchResponse, error)
	}{
		{"web", func(q string) (*types.SearchResponse, error) {
			return NewWebConnector(searxng.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
		{"news", func(q string) (*types.SearchResponse, error) {
			return NewNewsConnector(searxng.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
		{"youtube", func(q string) (*types.SearchResponse, error) {
			return NewYouTubeConnector(searxng.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
		{"images", func(q string) (*types.SearchResponse, error) {
			return NewImagesConnector(searxng.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
		{"academic", func(q string) (*types.SearchResponse, error) {
			return NewAcademicConnector(searxng.URL, cacheSvc).Search(ctx, &types.SearchRequest{Query: q})
		}},
	}

	for _, tc := range searxngConnectors {
		t.Run("searxng/"+tc.name, func(t *testing.T) {
			resp, err := tc.run("healthy-empty-" + tc.name)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.Partial {
				t.Errorf("expected Partial=false for healthy empty, got true")
			}
			if len(resp.Warnings) != 0 {
				t.Errorf("expected no warnings for healthy empty, got %v", resp.Warnings)
			}
		})
	}

	t.Run("reddit", func(t *testing.T) {
		c := NewRedditConnector(RedditConfig{BaseURL: reddit.URL}, cacheSvc)
		c.client = &http.Client{Timeout: 5 * time.Second}
		resp, err := c.Search(ctx, &types.SearchRequest{Query: "healthy-empty-reddit"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Partial {
			t.Errorf("expected Partial=false for healthy empty reddit, got true")
		}
		if len(resp.Warnings) != 0 {
			t.Errorf("expected no warnings for healthy empty reddit, got %v", resp.Warnings)
		}
	})
}
