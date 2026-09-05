package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

func TestRedditSearchUsesSearxngAndIsolatesCacheByLimit(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if got := r.URL.Query().Get("q"); got != "same-query site:reddit.com" {
			t.Errorf("unexpected SearXNG query: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	cacheSvc := cache.NewService(300)
	connector := NewRedditConnector(RedditConfig{SearxngURL: srv.URL}, cacheSvc)

	resp1, err := connector.Search(context.Background(), &types.SearchRequest{Query: "same-query", MaxResults: 3})
	if err != nil {
		t.Fatalf("populator Search: %v", err)
	}
	if resp1.Cached {
		t.Fatalf("first Search must NOT be a cache hit")
	}

	resp2, err := connector.Search(context.Background(), &types.SearchRequest{Query: "same-query", MaxResults: 10})
	if err != nil {
		t.Fatalf("reader Search: %v", err)
	}
	if resp2.Cached || requests != 2 {
		t.Fatalf("a different result limit must not reuse a cached response: cached=%v requests=%d", resp2.Cached, requests)
	}
}

func TestRedditSearchFiltersAndDeduplicatesIndexedResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"non-reddit","url":"https://example.com/post"},
			{"title":"first","url":"https://www.reddit.com/r/golang/comments/1/post/","content":"one"},
			{"title":"duplicate","url":"https://www.reddit.com/r/golang/comments/1/post"},
			{"title":"second","url":"https://old.reddit.com/r/golang/comments/2/post"}
		]}`))
	}))
	defer srv.Close()

	resp, err := NewRedditConnector(RedditConfig{SearxngURL: srv.URL}, cache.NewService(300)).Search(context.Background(), &types.SearchRequest{Query: "golang", MaxResults: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Title != "first" || resp.Results[0].CanonicalURL != "https://www.reddit.com/r/golang/comments/1/" {
		t.Fatalf("expected one canonical Reddit post, got %#v", resp.Results)
	}
}
