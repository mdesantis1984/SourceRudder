package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// pagedSearxng is an httptest SearXNG that serves a fixed list of URLs per
// page and records every request it receives.
type pagedSearxng struct {
	*httptest.Server
	mu       sync.Mutex
	pagenos  []string
	pages    map[string][]string // pageno -> result URLs ("" key = page 1 without pageno)
	engines  map[string]string   // pageno -> raw unresponsive_engines JSON
	failPage map[string]int      // pageno -> HTTP status to return
}

func newPagedSearxng(t *testing.T, pages map[string][]string) *pagedSearxng {
	t.Helper()
	p := &pagedSearxng{pages: pages, engines: map[string]string{}, failPage: map[string]int{}}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("pageno")
		if page == "" || page == "1" {
			page = "1"
		}
		p.mu.Lock()
		p.pagenos = append(p.pagenos, r.URL.Query().Get("pageno"))
		status := p.failPage[page]
		urls := p.pages[page]
		engines := p.engines[page]
		p.mu.Unlock()
		if status != 0 {
			http.Error(w, "boom", status)
			return
		}
		if engines == "" {
			engines = "[]"
		}
		items := make([]string, 0, len(urls))
		for i, u := range urls {
			items = append(items, fmt.Sprintf(`{"title":"t%d","url":%q,"content":"c","engine":"bing"}`, i, u))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[` + strings.Join(items, ",") + `],"unresponsive_engines":` + engines + `}`))
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *pagedSearxng) requests() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.pagenos...)
}

func urlRange(prefix string, from, to int) []string {
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, fmt.Sprintf("https://example.test/%s/%d", prefix, i))
	}
	return out
}

func resultURLs(resp *types.SearchResponse) []string {
	out := make([]string, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, r.URL)
	}
	return out
}

func assertStrictlyDescending(t *testing.T, resp *types.SearchResponse) {
	t.Helper()
	for i := 1; i < len(resp.Results); i++ {
		if !(resp.Results[i].Score < resp.Results[i-1].Score) {
			t.Fatalf("scores not strictly descending at %d: %v then %v", i, resp.Results[i-1].Score, resp.Results[i].Score)
		}
	}
}

func assertUnique(t *testing.T, resp *types.SearchResponse) {
	t.Helper()
	seen := map[string]bool{}
	for _, u := range resultURLs(resp) {
		if seen[u] {
			t.Fatalf("duplicate URL in results: %s", u)
		}
		seen[u] = true
	}
}

func TestWebSearchDefaultsToTwentyAndFillsFromPageTwo(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{
		"1": urlRange("a", 0, 12),
		"2": append(urlRange("a", 10, 12), urlRange("b", 0, 15)...), // 2 overlap page 1
	})
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "fill"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// S8: all 12 + 15 unique results are kept; the handler returns the first 20.
	if len(resp.Results) != 27 {
		t.Fatalf("expected every unique result (27), got %d", len(resp.Results))
	}
	if got := srv.requests(); len(got) != 2 || got[1] != "2" {
		t.Fatalf("expected exactly page 1 then pageno=2, got %v", got)
	}
	assertUnique(t, resp)
	assertStrictlyDescending(t, resp)
	if resp.Partial || len(resp.Warnings) != 0 {
		t.Fatalf("healthy fill must not be partial: %+v", resp)
	}
	// Page-1 order is preserved, page-2 items are appended.
	if resp.Results[0].URL != "https://example.test/a/0" || resp.Results[12].URL != "https://example.test/b/0" {
		t.Fatalf("unexpected order: %v", resultURLs(resp))
	}
}

func TestWebSearchNonPositiveMaxResultsUsesDefault(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 30)})
	c := NewWebConnector(srv.URL, cache.NewService(300))
	for _, n := range []int{0, -3} {
		resp, err := c.Search(context.Background(), &types.SearchRequest{Query: fmt.Sprintf("neg%d", n), MaxResults: n})
		// 30 unique results on page 1 satisfy the default target of 20: no
		// page 2, and the connector keeps all 30 (the handler slices to 20).
		if err != nil || len(resp.Results) != 30 {
			t.Fatalf("maxResults=%d: results=%d err=%v", n, len(resp.Results), err)
		}
		if got := srv.requests(); len(got) != 1 {
			t.Fatalf("maxResults=%d must use the default target of 20 and skip page 2, got %v", n, got)
		}
		srv.mu.Lock()
		srv.pagenos = nil
		srv.mu.Unlock()
	}
}

func TestWebSearchNoPageTwoWhenPageOneIsEnough(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 25), "2": urlRange("b", 0, 25)})
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "enough"})
	if err != nil || len(resp.Results) != 25 {
		t.Fatalf("results=%d err=%v", len(resp.Results), err)
	}
	if got := srv.requests(); len(got) != 1 {
		t.Fatalf("expected a single request, got %v", got)
	}
	assertStrictlyDescending(t, resp)
}

func TestWebSearchExplicitMaxResultsHonored(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 8), "2": urlRange("b", 0, 8)})
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "explicit", MaxResults: 5})
	if err != nil || len(resp.Results) != 8 {
		t.Fatalf("results=%d err=%v", len(resp.Results), err)
	}
	if got := srv.requests(); len(got) != 1 {
		t.Fatalf("explicit maxResults=5 with >=5 on page 1 must not request page 2, got %v", got)
	}
	assertStrictlyDescending(t, resp)
}

func TestWebSearchCapStaysAtOneHundred(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 60), "2": urlRange("b", 0, 60)})
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "cap", MaxResults: 500})
	// The target is capped at 100, so 60 page-1 results still trigger page 2;
	// every unique result is kept (the handler returns the first 100).
	if err != nil || len(resp.Results) != 120 {
		t.Fatalf("results=%d err=%v", len(resp.Results), err)
	}
	assertStrictlyDescending(t, resp)
}

func TestWebSearchDeduplicatesPageOneByURL(t *testing.T) {
	dup := urlRange("a", 0, 3)
	srv := newPagedSearxng(t, map[string][]string{"1": append(append([]string{}, dup...), dup...)})
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "dedup", MaxResults: 3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 3 {
		t.Fatalf("expected 3 unique results, got %v", resultURLs(resp))
	}
	assertUnique(t, resp)
	if got := srv.requests(); len(got) != 1 {
		t.Fatalf("3 unique results satisfy maxResults=3, got requests %v", got)
	}
}

func TestWebSearchDuplicatesDoNotCountTowardMaxResults(t *testing.T) {
	dup := urlRange("a", 0, 3)
	srv := newPagedSearxng(t, map[string][]string{
		"1": append(append([]string{}, dup...), dup...), // 6 raw, 3 unique
		"2": append(append([]string{}, dup...), urlRange("b", 0, 5)...),
	})
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "dedup2", MaxResults: 5})
	if err != nil || len(resp.Results) != 8 {
		t.Fatalf("results=%v err=%v", resultURLs(resp), err)
	}
	assertUnique(t, resp)
	assertStrictlyDescending(t, resp)
	if got := srv.requests(); len(got) != 2 || got[1] != "2" {
		t.Fatalf("expected page 1 then pageno=2, got %v", got)
	}
}

func TestWebSearchPageTwoFailureKeepsPageOneWithWarning(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 4)})
	srv.failPage["2"] = http.StatusInternalServerError
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "page2fail"})
	if err != nil {
		t.Fatalf("page-2 failure must never fail the call: %v", err)
	}
	if len(resp.Results) != 4 {
		t.Fatalf("expected the 4 page-1 results, got %v", resultURLs(resp))
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "page 2") {
		t.Fatalf("expected one page-2 warning, got %v", resp.Warnings)
	}
	if got := srv.requests(); len(got) != 2 {
		t.Fatalf("page 2 must be requested exactly once, got %v", got)
	}
	assertStrictlyDescending(t, resp)
}

func TestWebSearchPageOneFailureStillReportsPartial(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{})
	srv.failPage["1"] = http.StatusBadGateway
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "page1fail"})
	if err != nil || len(resp.Results) != 0 || !resp.Partial || len(resp.Warnings) != 1 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if got := srv.requests(); len(got) != 1 {
		t.Fatalf("no page 2 after a page-1 failure, got %v", got)
	}
}

func TestWebSearchReportsUnresponsiveEnginesFromBothPagesOnce(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 3), "2": urlRange("b", 0, 3)})
	srv.engines["1"] = `[["brave","HTTP error"]]`
	srv.engines["2"] = `[["brave","HTTP error"],["bing","timeout"]]`
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "engines"})
	if err != nil || len(resp.Results) != 6 {
		t.Fatalf("results=%d err=%v", len(resp.Results), err)
	}
	if !resp.Partial || len(resp.Warnings) != 1 {
		t.Fatalf("expected partial with one warning, got %+v", resp)
	}
	w := resp.Warnings[0]
	if strings.Count(w, "brave") != 1 || strings.Count(w, "bing") != 1 {
		t.Fatalf("engines must be reported once each: %s", w)
	}
}

func TestWebSearchReportsEnginesOnlyPageTwoReports(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 3), "2": urlRange("b", 0, 3)})
	srv.engines["2"] = `[["bing","timeout"]]`
	c := NewWebConnector(srv.URL, cache.NewService(300))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "engines2"})
	if err != nil || !resp.Partial || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "bing") {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestWebSearchCacheKeyStillDistinguishesRequests(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 30)})
	c := NewWebConnector(srv.URL, cache.NewService(300))
	ctx := context.Background()
	first, _ := c.Search(ctx, &types.SearchRequest{Query: "keys"})
	again, _ := c.Search(ctx, &types.SearchRequest{Query: "keys"})
	if first.Cached || !again.Cached || len(again.Results) != 30 {
		t.Fatalf("identical request must hit cache: first=%v again=%v n=%d", first.Cached, again.Cached, len(again.Results))
	}
	other, _ := c.Search(ctx, &types.SearchRequest{Query: "keys", MaxResults: 5})
	if other.Cached || len(other.Results) != 30 {
		t.Fatalf("different maxResults must not collide: cached=%v n=%d", other.Cached, len(other.Results))
	}
}

func cachedTTLs(t *testing.T, svc *cache.Service) []time.Duration {
	t.Helper()
	ctx := context.Background()
	keys, _ := svc.Keys(ctx)
	out := make([]time.Duration, 0, len(keys))
	for _, k := range keys {
		e, ok, _ := svc.Get(ctx, k)
		if !ok {
			t.Fatalf("key %s vanished", k)
		}
		var resp types.SearchResponse
		_ = json.Unmarshal(e.Payload, &resp)
		out = append(out, e.ExpiresAt.Sub(e.CreatedAt))
	}
	return out
}

func TestWebSearchEmptyResultIsCachedForAtMostThirtySeconds(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{})
	svc := cache.NewService(300)
	resp, err := NewWebConnector(srv.URL, svc).Search(context.Background(), &types.SearchRequest{Query: "nothing"})
	if err != nil || len(resp.Results) != 0 || resp.Partial {
		t.Fatalf("healthy empty expected, resp=%+v err=%v", resp, err)
	}
	ttls := cachedTTLs(t, svc)
	if len(ttls) != 1 || ttls[0] > DegradedSearchCacheTTL {
		t.Fatalf("empty response TTL must be <= %v, got %v", DegradedSearchCacheTTL, ttls)
	}
}

func TestWebSearchNonEmptyHealthyResultKeepsNormalTTL(t *testing.T) {
	srv := newPagedSearxng(t, map[string][]string{"1": urlRange("a", 0, 25)})
	svc := cache.NewService(300)
	if _, err := NewWebConnector(srv.URL, svc).Search(context.Background(), &types.SearchRequest{Query: "full"}); err != nil {
		t.Fatal(err)
	}
	if ttls := cachedTTLs(t, svc); len(ttls) != 1 || ttls[0] != 300*time.Second {
		t.Fatalf("healthy non-empty TTL must stay 300s, got %v", ttls)
	}
}

// S3 applies to every SearXNG-backed connector: the news connector shares
// the cache helper, so it gets the 30 s empty cap but keeps the normal TTL
// for a non-empty healthy response.
func TestNewsSearchEmptyCappedAndNonEmptyKeepsNormalTTL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results string
		want    time.Duration
	}{
		{"empty", `[]`, DegradedSearchCacheTTL},
		{"non-empty", `[{"title":"n","url":"https://example.test/n","content":"c","engine":"bing"}]`, 300 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":` + tc.results + `,"unresponsive_engines":[]}`))
			}))
			defer srv.Close()
			svc := cache.NewService(300)
			resp, err := NewNewsConnector(srv.URL, svc).Search(context.Background(), &types.SearchRequest{Query: "news-ttl"})
			if err != nil || resp.Partial {
				t.Fatalf("resp=%+v err=%v", resp, err)
			}
			if ttls := cachedTTLs(t, svc); len(ttls) != 1 || ttls[0] != tc.want {
				t.Fatalf("TTL want %v, got %v", tc.want, ttls)
			}
		})
	}
}
