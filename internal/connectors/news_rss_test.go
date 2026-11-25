package connectors

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// gnewsRSS builds a Google News style RSS body. Each entry is {title, link,
// publisher}.
func gnewsRSS(entries ...[3]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel><title>Google News</title>`)
	for i, e := range entries {
		fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><pubDate>Mon, 02 Nov 2026 %02d:00:00 GMT</pubDate>`+
			`<description>&lt;a href="%s"&gt;%s&lt;/a&gt;&amp;nbsp;&amp;nbsp;&lt;font color="#6f6f6f"&gt;%s&lt;/font&gt;</description>`+
			`<source url="https://%s.example">%s</source></item>`,
			e[0], e[1], 10+i, e[1], e[0], e[2], strings.ToLower(strings.ReplaceAll(e[2], " ", "")), e[2])
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func searxngJSON(n int, engine string, unresponsive string) string {
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, fmt.Sprintf(`{"title":"Searx story %d","url":"https://searx.example/story-%d","content":"c","engine":"%s"}`, i, i, engine))
	}
	if unresponsive == "" {
		unresponsive = "[]"
	}
	return `{"results":[` + strings.Join(parts, ",") + `],"unresponsive_engines":` + unresponsive + `}`
}

type newsFixture struct {
	searx    *httptest.Server
	rss      *httptest.Server
	rssHits  atomic.Int32
	mu       sync.Mutex
	rssQuery url.Values
}

func newNewsFixture(t *testing.T, searxBody string, searxStatus int, rssBody string, rssStatus int) *newsFixture {
	t.Helper()
	f := &newsFixture{}
	f.searx = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if searxStatus != 0 && searxStatus != 200 {
			http.Error(w, "boom", searxStatus)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searxBody))
	}))
	f.rss = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.rssHits.Add(1)
		f.mu.Lock()
		f.rssQuery = r.URL.Query()
		f.mu.Unlock()
		if rssStatus != 0 && rssStatus != 200 {
			http.Error(w, "boom", rssStatus)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rssBody))
	}))
	t.Cleanup(func() { f.searx.Close(); f.rss.Close() })
	return f
}

func (f *newsFixture) connector() *NewsConnector {
	c := NewNewsConnector(f.searx.URL, cache.NewService(300))
	c.SetGoogleNewsRSS(f.rss.URL)
	return c
}

func (f *newsFixture) query() url.Values {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rssQuery
}

func TestNewsSupplementsWithGoogleNewsRSSWhenThin(t *testing.T) {
	rss := gnewsRSS(
		[3]string{"Searx story 0 - Dup Publisher", "https://news.google.example/a", "Dup Publisher"},
		[3]string{"Fresh Story One - Daily Planet", "https://news.google.example/b", "Daily Planet"},
		[3]string{"Fresh story two", "https://searx.example/story-1", "Other"},
		[3]string{"Fresh Story Three - The Wire", "https://news.google.example/c", "The Wire"},
	)
	f := newNewsFixture(t, searxngJSON(2, "bing news", ""), 200, rss, 200)
	resp, err := f.connector().Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5, Language: "es-AR", TimeRange: "week"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Partial || len(resp.Warnings) != 0 {
		t.Fatalf("a healthy supplement is not partial and adds no warning: %+v", resp)
	}
	var titles []string
	for _, r := range resp.Results {
		titles = append(titles, r.Title)
	}
	want := []string{"Searx story 0", "Searx story 1", "Fresh Story One - Daily Planet", "Fresh Story Three - The Wire"}
	if strings.Join(titles, "|") != strings.Join(want, "|") {
		t.Fatalf("merge/dedupe order wrong:\n got %q\nwant %q", titles, want)
	}
	for i := 1; i < len(resp.Results); i++ {
		if !(resp.Results[i].Score < resp.Results[i-1].Score) {
			t.Fatalf("scores must strictly decrease: %v then %v at %d", resp.Results[i-1].Score, resp.Results[i].Score, i)
		}
	}
	g := resp.Results[2]
	if g.Engine != "google news rss" || g.Source != "Daily Planet" || g.URL != "https://news.google.example/b" || g.PublishedAt == nil || g.Type != "article" {
		t.Errorf("google news item = %+v", g)
	}
	if !strings.HasPrefix(g.CitationID, "news:") {
		t.Errorf("citation id = %q", g.CitationID)
	}
	q := f.query()
	if q.Get("q") != "kubernetes when:7d" || q.Get("hl") != "es-419" || q.Get("gl") != "AR" || q.Get("ceid") != "AR:es-419" {
		t.Errorf("rss query = %v", q)
	}
}

func TestNewsDoesNotCallRSSWhenSearxngIsFullAndHealthy(t *testing.T) {
	f := newNewsFixture(t, searxngJSON(5, "bing news", ""), 200, gnewsRSS([3]string{"x", "https://n.example/x", "P"}), 200)
	resp, err := f.connector().Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5})
	if err != nil || len(resp.Results) != 5 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if f.rssHits.Load() != 0 {
		t.Fatalf("RSS was called %d times although SearXNG had enough healthy results", f.rssHits.Load())
	}
}

func TestNewsCallsRSSWhenSearxngIsPartialEvenIfFull(t *testing.T) {
	f := newNewsFixture(t, searxngJSON(5, "bing news", `[["reuters","HTTP error"]]`), 200,
		gnewsRSS([3]string{"Extra - P", "https://n.example/extra", "P"}), 200)
	resp, err := f.connector().Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	if f.rssHits.Load() != 1 || len(resp.Results) != 6 || !resp.Partial {
		t.Fatalf("hits=%d results=%d partial=%v warnings=%v", f.rssHits.Load(), len(resp.Results), resp.Partial, resp.Warnings)
	}
	if resp.Results[5].Engine != "google news rss" {
		t.Errorf("last result = %+v", resp.Results[5])
	}
}

func TestNewsRSSFailureKeepsSearxngResultsAndWarns(t *testing.T) {
	f := newNewsFixture(t, searxngJSON(2, "bing news", ""), 200, "", 503)
	resp, err := f.connector().Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5})
	if err != nil || len(resp.Results) != 2 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if resp.Partial {
		t.Error("SearXNG answered fine; a failed supplement must not mark the response partial")
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "google news rss") || !strings.Contains(resp.Warnings[0], "503") {
		t.Errorf("warnings = %v", resp.Warnings)
	}
}

func TestNewsBothFailBehavesAsBeforePlusRSSWarning(t *testing.T) {
	f := newNewsFixture(t, "", 500, "", 503)
	resp, err := f.connector().Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5, TimeRange: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 0 || !resp.Partial {
		t.Fatalf("resp = %+v", resp)
	}
	if len(resp.Warnings) != 2 || !strings.Contains(resp.Warnings[0], "searxng error: 500") || !strings.Contains(resp.Warnings[1], "google news rss") {
		t.Errorf("warnings = %v", resp.Warnings)
	}
}

func TestNewsRSSFillsEmptySearxngWithoutTimeRangeWarning(t *testing.T) {
	f := newNewsFixture(t, searxngJSON(0, "bing news", ""), 200, gnewsRSS([3]string{"Only - P", "https://n.example/only", "P"}), 200)
	resp, err := f.connector().Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5, TimeRange: "day"})
	if err != nil || len(resp.Results) != 1 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	for _, w := range resp.Warnings {
		if strings.Contains(w, "timeRange") {
			t.Errorf("results exist, the timeRange warning would be dishonest: %v", resp.Warnings)
		}
	}
	if q := f.query(); q.Get("q") != "kubernetes when:1d" || q.Get("hl") != "en-US" || q.Get("ceid") != "US:en" {
		t.Errorf("rss query = %v", q)
	}
}

func TestNewsRSSEmptyKeepsTimeRangeWarning(t *testing.T) {
	f := newNewsFixture(t, searxngJSON(0, "bing news", ""), 200, gnewsRSS(), 200)
	resp, err := f.connector().Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5, TimeRange: "day"})
	if err != nil || len(resp.Results) != 0 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0] != TimeRangeEmptyWarning {
		t.Errorf("warnings = %v", resp.Warnings)
	}
}

func TestNewsWithoutRSSConfiguredNeverCallsOut(t *testing.T) {
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(searxngJSON(1, "bing news", "")))
	}))
	defer searx.Close()
	resp, err := NewNewsConnector(searx.URL, cache.NewService(300)).Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5})
	if err != nil || len(resp.Results) != 1 || len(resp.Warnings) != 0 {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestNewsRSSResultsAreCached(t *testing.T) {
	f := newNewsFixture(t, searxngJSON(1, "bing news", ""), 200, gnewsRSS([3]string{"A - P", "https://n.example/a", "P"}), 200)
	c := f.connector()
	req := &types.SearchRequest{Query: "kubernetes", MaxResults: 5}
	if _, err := c.Search(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	resp, err := c.Search(context.Background(), req)
	if err != nil || !resp.Cached || f.rssHits.Load() != 1 {
		t.Fatalf("second call must come from cache: cached=%v hits=%d err=%v", resp.Cached, f.rssHits.Load(), err)
	}
}

func TestNewsRSSFailureIsCachedOnlyBriefly(t *testing.T) {
	f := newNewsFixture(t, searxngJSON(1, "bing news", ""), 200, "", 503)
	c := f.connector()
	if _, err := c.Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5}); err != nil {
		t.Fatal(err)
	}
	keys, _ := c.cacheSvc.Keys(context.Background())
	if len(keys) != 1 {
		t.Fatalf("keys = %v", keys)
	}
	entry, ok, _ := c.cacheSvc.Get(context.Background(), keys[0])
	if !ok || entry.ExpiresAt.Sub(entry.CreatedAt) > DegradedSearchCacheTTL {
		t.Fatalf("a response with a failed supplement must expire within %v, got %v", DegradedSearchCacheTTL, entry.ExpiresAt.Sub(entry.CreatedAt))
	}
}
