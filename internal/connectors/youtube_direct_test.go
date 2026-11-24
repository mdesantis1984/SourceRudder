package connectors

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/youtube"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type fakeDirectSearch struct {
	calls   atomic.Int32
	query   string
	limit   int
	entries []youtube.SearchEntry
	err     error
}

func (f *fakeDirectSearch) Search(_ context.Context, query string, limit int) ([]youtube.SearchEntry, error) {
	f.calls.Add(1)
	f.query, f.limit = query, limit
	return f.entries, f.err
}

const searxngVideoBody = `{"results":[{"title":"searxng video","url":"https://www.youtube.com/watch?v=sxsxsxsxsx1","content":"from searxng","engine":"youtube"}],"unresponsive_engines":[]}`

// searxngStub counts hits and records the engines parameter.
func searxngStub(t *testing.T, body string) (*httptest.Server, *atomic.Int32, *atomic.Value) {
	t.Helper()
	var hits atomic.Int32
	var engines atomic.Value
	engines.Store("")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		engines.Store(r.URL.Query().Get("engines"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &engines
}

func directEntries() []youtube.SearchEntry {
	return []youtube.SearchEntry{
		{ID: "uiUCIz-3CWM", Title: "Context tutorial", URL: "https://www.youtube.com/watch?v=uiUCIz-3CWM", Channel: "Melkey",
			Description: "Code and more", DurationSeconds: 3725, Views: 60139, PublishedAt: time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)},
		{ID: "abcdefghijk", Title: "Bare", URL: "https://www.youtube.com/watch?v=abcdefghijk"},
	}
}

func TestYouTubeDirectSearchMapsEntries(t *testing.T) {
	srv, hits, _ := searxngStub(t, searxngVideoBody)
	fake := &fakeDirectSearch{entries: directEntries()}
	c := NewYouTubeConnector(srv.URL, cache.NewService(60))
	c.SetDirectSearch(fake)

	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "  golang context tutorial ", MaxResults: 7})
	if err != nil {
		t.Fatal(err)
	}
	if fake.query != "golang context tutorial" || fake.limit != 7 {
		t.Errorf("yt-dlp asked for %q x%d, want the trimmed query x7", fake.query, fake.limit)
	}
	if hits.Load() != 0 {
		t.Errorf("SearXNG hit %d times, want 0 when yt-dlp answers", hits.Load())
	}
	if resp.Partial || len(resp.Warnings) != 0 || len(resp.Errors) != 0 {
		t.Errorf("clean direct search must not degrade: partial=%v warnings=%q errors=%v", resp.Partial, resp.Warnings, resp.Errors)
	}
	if len(resp.SourcesUsed) != 1 || resp.SourcesUsed[0] != "youtube" {
		t.Errorf("sourcesUsed = %v", resp.SourcesUsed)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(resp.Results))
	}
	r := resp.Results[0]
	if r.Title != "Context tutorial" || r.URL != "https://www.youtube.com/watch?v=uiUCIz-3CWM" ||
		r.Engine != "yt-dlp" || r.Source != "yt-dlp" || r.Type != "video" ||
		r.CitationID != "youtube:uiUCIz-3CWM" || r.Author != "Melkey" {
		t.Errorf("result = %+v", r)
	}
	if want := "Melkey · 1:02:05 · 60,139 views · Code and more"; r.Snippet != want {
		t.Errorf("snippet = %q, want %q", r.Snippet, want)
	}
	if r.PublishedAt == nil || !r.PublishedAt.Equal(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("publishedAt = %v", r.PublishedAt)
	}
	b := resp.Results[1]
	if b.Snippet != "" || b.PublishedAt != nil || b.Author != "" {
		t.Errorf("bare entry must not invent metadata: %+v", b)
	}
	if r.Score <= b.Score {
		t.Errorf("order must be kept: scores %v then %v", r.Score, b.Score)
	}
}

func TestYouTubeDirectSearchSnippetFormats(t *testing.T) {
	tests := []struct {
		name  string
		entry youtube.SearchEntry
		want  string
	}{
		{"short duration", youtube.SearchEntry{Channel: "C", DurationSeconds: 125, Views: 1}, "C · 2:05 · 1 views"},
		{"channel only", youtube.SearchEntry{Channel: "C"}, "C"},
		{"description only", youtube.SearchEntry{Description: "d"}, "d"},
		{"long description is cut by runes", youtube.SearchEntry{Description: strings.Repeat("é", 400)}, strings.Repeat("é", 300) + "..."},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.entry.ID, tc.entry.Title, tc.entry.URL = "abcdefghijk", "t", "https://www.youtube.com/watch?v=abcdefghijk"
			c := NewYouTubeConnector("http://127.0.0.1:1", cache.NewService(60))
			c.SetDirectSearch(&fakeDirectSearch{entries: []youtube.SearchEntry{tc.entry}})
			resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "snippet-" + tc.name})
			if err != nil || len(resp.Results) != 1 {
				t.Fatalf("resp=%+v err=%v", resp, err)
			}
			if got := resp.Results[0].Snippet; got != tc.want {
				t.Errorf("snippet = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestYouTubeDirectSearchFallsBackToSearXNG(t *testing.T) {
	tests := []struct {
		name        string
		fake        *fakeDirectSearch
		wantWarning string
	}{
		{"error", &fakeDirectSearch{err: errors.New("youtube_error: yt-dlp failed: ERROR: boom")},
			"yt-dlp search failed, used SearXNG fallback: youtube_error: yt-dlp failed: ERROR: boom"},
		{"timeout", &fakeDirectSearch{err: errors.New("youtube_timeout: yt-dlp did not finish within 30s")},
			"yt-dlp search failed, used SearXNG fallback: youtube_timeout: yt-dlp did not finish within 30s"},
		{"unavailable", &fakeDirectSearch{err: youtube.ErrUnavailable},
			"yt-dlp search failed, used SearXNG fallback: " + youtube.ErrUnavailable.Error()},
		{"empty", &fakeDirectSearch{}, "yt-dlp search returned no results, used SearXNG fallback"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			met := observability.New()
			observability.SetDefault(met)
			srv, hits, engines := searxngStub(t, searxngVideoBody)
			c := NewYouTubeConnector(srv.URL, cache.NewService(60))
			c.SetDirectSearch(tc.fake)
			resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "fallback-" + tc.name})
			if err != nil {
				t.Fatal(err)
			}
			if hits.Load() != 1 || engines.Load().(string) != "youtube,brave" {
				t.Errorf("SearXNG hits=%d engines=%q, want one call with youtube,brave", hits.Load(), engines.Load())
			}
			if len(resp.Results) != 1 || resp.Results[0].Title != "searxng video" || resp.Results[0].Engine != "youtube" {
				t.Fatalf("results = %+v", resp.Results)
			}
			if !slices.Contains(resp.Warnings, tc.wantWarning) {
				t.Errorf("warnings = %q, want %q", resp.Warnings, tc.wantWarning)
			}
			if resp.Partial {
				t.Errorf("a working fallback is not partial")
			}
			if got := readDegradedCounter(t, met, "youtube", "ytdlp_fallback"); got != 1 {
				t.Errorf("fallback metric = %v, want 1", got)
			}
		})
	}
}

func TestYouTubeDirectFallbackAlsoFailingKeepsBothReasons(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer broken.Close()
	c := NewYouTubeConnector(broken.URL, cache.NewService(60))
	c.SetDirectSearch(&fakeDirectSearch{err: errors.New("yt boom")})
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "both-fail"})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Partial || len(resp.Results) != 0 {
		t.Fatalf("partial=%v results=%d, want partial and empty", resp.Partial, len(resp.Results))
	}
	if len(resp.Warnings) != 2 || !strings.Contains(resp.Warnings[0], "yt boom") || !strings.Contains(resp.Warnings[1], "searxng error: 500") {
		t.Errorf("warnings = %q", resp.Warnings)
	}
}

func TestYouTubeTimeRangeSkipsDirectSearch(t *testing.T) {
	srv, hits, _ := searxngStub(t, searxngVideoBody)
	fake := &fakeDirectSearch{entries: directEntries()}
	c := NewYouTubeConnector(srv.URL, cache.NewService(60))
	c.SetDirectSearch(fake)
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "tr-direct", TimeRange: "week"})
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls.Load() != 0 {
		t.Errorf("yt-dlp ran %d times with timeRange set, want 0 (ytsearch has no date filter)", fake.calls.Load())
	}
	if hits.Load() != 1 || len(resp.Results) != 1 || resp.Results[0].Title != "searxng video" {
		t.Errorf("hits=%d results=%+v", hits.Load(), resp.Results)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %q, want none: skipping yt-dlp is not a failure", resp.Warnings)
	}
}

func TestYouTubeTimeRangeEmptyStillWarnsWithDirectSearch(t *testing.T) {
	srv, _, _ := searxngStub(t, `{"results":[],"unresponsive_engines":[]}`)
	c := NewYouTubeConnector(srv.URL, cache.NewService(60))
	c.SetDirectSearch(&fakeDirectSearch{entries: directEntries()})
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "tr-direct-empty", TimeRange: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(resp.Warnings, wantTimeRangeWarning) {
		t.Errorf("warnings = %q, want the timeRange warning", resp.Warnings)
	}
}

func TestYouTubeWithoutDirectSearchIsSearXNGOnly(t *testing.T) {
	srv, hits, engines := searxngStub(t, searxngVideoBody)
	c := NewYouTubeConnector(srv.URL, cache.NewService(60))
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || engines.Load().(string) != "youtube,brave" {
		t.Errorf("hits=%d engines=%q", hits.Load(), engines.Load())
	}
	if len(resp.Results) != 1 || len(resp.Warnings) != 0 || resp.Partial {
		t.Errorf("resp = %+v", resp)
	}
}

func TestYouTubeDirectSearchIsCached(t *testing.T) {
	srv, _, _ := searxngStub(t, searxngVideoBody)
	fake := &fakeDirectSearch{entries: directEntries()}
	c := NewYouTubeConnector(srv.URL, cache.NewService(60))
	c.SetDirectSearch(fake)
	req := &types.SearchRequest{Query: "cache-direct", MaxResults: 5}
	first, err := c.Search(context.Background(), req)
	if err != nil || first.Cached {
		t.Fatalf("first: cached=%v err=%v", first.Cached, err)
	}
	second, err := c.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached || len(second.Results) != 2 || second.Results[0].Engine != "yt-dlp" {
		t.Errorf("second = %+v", second)
	}
	if fake.calls.Load() != 1 {
		t.Errorf("yt-dlp calls = %d, want 1", fake.calls.Load())
	}
	third, _ := c.Search(context.Background(), &types.SearchRequest{Query: "cache-direct", MaxResults: 6})
	if third.Cached || fake.calls.Load() != 2 {
		t.Errorf("a different maxResults must miss the cache: cached=%v calls=%d", third.Cached, fake.calls.Load())
	}
}

func TestYouTubeFallbackResponseIsCachedBriefly(t *testing.T) {
	srv, hits, _ := searxngStub(t, searxngVideoBody)
	fake := &fakeDirectSearch{err: errors.New("boom")}
	svc := cache.NewService(3600)
	c := NewYouTubeConnector(srv.URL, svc)
	c.SetDirectSearch(fake)
	req := &types.SearchRequest{Query: "cache-fallback"}
	if _, err := c.Search(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	second, _ := c.Search(context.Background(), req)
	if !second.Cached || hits.Load() != 1 {
		t.Fatalf("second call must come from cache: cached=%v hits=%d", second.Cached, hits.Load())
	}
	if !slices.Contains(second.Warnings, "yt-dlp search failed, used SearXNG fallback: boom") {
		t.Errorf("cached warnings = %q", second.Warnings)
	}
	key := c.cacheKey(strings.TrimSpace(req.Query), getMaxResults(req.MaxResults, 10), req)
	entry, ok, _ := svc.Get(context.Background(), key)
	if !ok {
		t.Fatal("fallback response was not cached")
	}
	if ttl := time.Until(entry.ExpiresAt); ttl > DegradedSearchCacheTTL+time.Second {
		t.Errorf("fallback cached for %v, want at most %v so yt-dlp is retried soon", ttl, DegradedSearchCacheTTL)
	}
}
