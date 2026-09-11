package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func TestRedditSearchForwardsSearxngOptionsAndReturnsOnlyCanonicalPosts(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/search" {
			t.Fatalf("search_reddit must call SearXNG /search, got %q", r.URL.Path)
		}
		params := r.URL.Query()
		if params.Get("q") != "golang site:reddit.com" || params.Get("language") != "es" || params.Get("time_range") != "week" || params.Get("safesearch") != "1" {
			t.Fatalf("SearXNG options were not forwarded: %v", params)
		}
		_, _ = w.Write([]byte(`{"results":[
			{"title":"profile","url":"https://www.reddit.com/user/example"},
			{"title":"thread one","url":"https://old.reddit.com/r/golang/comments/a1b2/thread-one/?utm_source=test#comment"},
			{"title":"thread two","url":"https://www.reddit.com/r/golang/comments/c3d4/thread-two"},
			{"title":"thread three","url":"https://np.reddit.com/r/golang/comments/e5f6/thread-three"},
			{"title":"thread four","url":"https://reddit.com/r/golang/comments/g7h8/thread-four"},
			{"title":"thread five","url":"https://www.reddit.com/r/golang/comments/i9j0/thread-five"}
		]}`))
	}))
	defer srv.Close()

	resp, err := NewRedditConnector(RedditConfig{SearxngURL: srv.URL}, cache.NewService(60)).Search(context.Background(), &types.SearchRequest{
		Query: "golang", MaxResults: 3, Language: "es", TimeRange: "week", SafeSearch: true,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if calls != 1 || len(resp.Results) != 3 {
		t.Fatalf("expected one SearXNG call and three bounded posts, calls=%d results=%d", calls, len(resp.Results))
	}
	wantURLs := []string{
		"https://www.reddit.com/r/golang/comments/a1b2/",
		"https://www.reddit.com/r/golang/comments/c3d4/",
		"https://www.reddit.com/r/golang/comments/e5f6/",
	}
	for i, want := range wantURLs {
		if resp.Results[i].URL != want || resp.Results[i].CanonicalURL != want || resp.Results[i].CitationID != "reddit:"+want {
			t.Fatalf("result %d is not canonical: %#v", i, resp.Results[i])
		}
	}
}

func TestCanonicalRedditPostURLRejectsNonThreadTargets(t *testing.T) {
	for _, rawURL := range []string{
		"ftp://reddit.com/r/golang/comments/a1b2/post",
		"https://user@reddit.com/r/golang/comments/a1b2/post",
		"https://reddit.com:443/r/golang/comments/a1b2/post",
		"https://evil.reddit.com/r/golang/comments/a1b2/post",
		"https://reddit.com/r/golang",
		"https://reddit.com/search?q=golang",
		"https://reddit.com/r/golang/comments/not-an-id!/post",
		"https://reddit.com/r/golang/comments/%2F/post",
	} {
		if canonical, ok := canonicalRedditPostURL(rawURL); ok {
			t.Errorf("accepted non-thread URL %q as %q", rawURL, canonical)
		}
	}
}

func TestRedditSearchCacheOptionsAndProviderOutcomes(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Query().Get("q") {
		case "malformed site:reddit.com":
			_, _ = w.Write([]byte(`{`))
		case "rate site:reddit.com":
			w.WriteHeader(http.StatusTooManyRequests)
		case "unavailable site:reddit.com":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			_, _ = w.Write([]byte(`{"results":null}`))
		}
	}))
	defer srv.Close()

	connector := NewRedditConnector(RedditConfig{SearxngURL: srv.URL}, cache.NewService(60))
	base := &types.SearchRequest{Query: "cached", MaxResults: 3, Language: "en", SafeSearch: true, TimeRange: "day", Filters: map[string]string{"scope": "posts"}}
	first, err := connector.Search(context.Background(), base)
	if err != nil || first.Cached || first.Partial || len(first.Results) != 0 {
		t.Fatalf("expected healthy empty first response, resp=%#v err=%v", first, err)
	}
	second, err := connector.Search(context.Background(), base)
	if err != nil || !second.Cached || calls != 1 {
		t.Fatalf("expected matching options to hit cache, cached=%v calls=%d err=%v", second.Cached, calls, err)
	}
	for _, changed := range []*types.SearchRequest{
		{Query: "cached", MaxResults: 10, Language: "en", SafeSearch: true, TimeRange: "day", Filters: map[string]string{"scope": "posts"}},
		{Query: "cached", MaxResults: 3, Language: "es", SafeSearch: true, TimeRange: "day", Filters: map[string]string{"scope": "posts"}},
		{Query: "cached", MaxResults: 3, Language: "en", SafeSearch: false, TimeRange: "day", Filters: map[string]string{"scope": "posts"}},
		{Query: "cached", MaxResults: 3, Language: "en", SafeSearch: true, TimeRange: "week", Filters: map[string]string{"scope": "posts"}},
	} {
		resp, err := connector.Search(context.Background(), changed)
		if err != nil || resp.Cached {
			t.Fatalf("changed cache options must miss cache: resp=%#v err=%v", resp, err)
		}
	}
	var rateWarning string
	for _, query := range []string{"malformed", "rate", "unavailable"} {
		resp, err := connector.Search(context.Background(), &types.SearchRequest{Query: query})
		if err != nil || !resp.Partial || len(resp.Warnings) != 1 || len(resp.Errors) != 0 {
			t.Fatalf("%s must retain a partial warning without inventing errors: resp=%#v err=%v", query, resp, err)
		}
		if query == "rate" {
			rateWarning = resp.Warnings[0]
		}
	}
	if !strings.Contains(rateWarning, "429") {
		t.Fatal("429 warning was not preserved")
	}
}
