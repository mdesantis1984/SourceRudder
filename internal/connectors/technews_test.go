package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

var techNow = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func rssAt(items ...[3]string) string { // {title, link, rfc1123 date}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>t</title>`)
	for _, it := range items {
		b.WriteString("<item><title>" + it[0] + "</title><link>" + it[1] + "</link>")
		if it[2] != "" {
			b.WriteString("<pubDate>" + it[2] + "</pubDate>")
		}
		b.WriteString("<description>" + it[0] + " summary</description></item>")
	}
	b.WriteString("</channel></rss>")
	return b.String()
}

func ago(d time.Duration) string { return techNow.Add(-d).Format(time.RFC1123Z) }

type techFixture struct {
	hn, gn         *httptest.Server
	feeds          []*httptest.Server
	hnHits, gnHits atomic.Int32
	feedHits       atomic.Int32
	mu             sync.Mutex
	hnQuery        url.Values
	gnQuery        url.Values
	cfg            TechNewsConfig
}

type feedSpec struct {
	name   string
	body   string
	status int
	delay  time.Duration
}

func newTechFixture(t *testing.T, hnBody string, hnStatus int, gnBody string, gnStatus int, feeds ...feedSpec) *techFixture {
	t.Helper()
	f := &techFixture{}
	f.hn = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hnHits.Add(1)
		f.mu.Lock()
		f.hnQuery = r.URL.Query()
		f.mu.Unlock()
		if hnStatus != 200 {
			http.Error(w, "boom", hnStatus)
			return
		}
		_, _ = w.Write([]byte(hnBody))
	}))
	f.gn = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.gnHits.Add(1)
		f.mu.Lock()
		f.gnQuery = r.URL.Query()
		f.mu.Unlock()
		if gnStatus != 200 {
			http.Error(w, "boom", gnStatus)
			return
		}
		_, _ = w.Write([]byte(gnBody))
	}))
	f.cfg = TechNewsConfig{HNURL: f.hn.URL, GoogleNewsURL: f.gn.URL, Feeds: []TechFeed{}, Now: func() time.Time { return techNow }}
	for _, spec := range feeds {
		spec := spec
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f.feedHits.Add(1)
			if spec.delay > 0 {
				select {
				case <-time.After(spec.delay):
				case <-r.Context().Done():
					return
				}
			}
			if spec.status != 0 && spec.status != 200 {
				http.Error(w, "boom", spec.status)
				return
			}
			_, _ = w.Write([]byte(spec.body))
		}))
		f.feeds = append(f.feeds, srv)
		f.cfg.Feeds = append(f.cfg.Feeds, TechFeed{Name: spec.name, URL: srv.URL})
	}
	t.Cleanup(func() {
		f.hn.Close()
		f.gn.Close()
		for _, s := range f.feeds {
			s.Close()
		}
	})
	return f
}

func (f *techFixture) search(t *testing.T, req *types.SearchRequest) *types.SearchResponse {
	t.Helper()
	resp, err := NewTechNewsConnector(cache.NewService(300), f.cfg).Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

const emptyHN = `{"hits":[]}`

func hnJSON(hits ...string) string { return `{"hits":[` + strings.Join(hits, ",") + `]}` }

func hnHit(id, title, urlField, author string, points, comments int, created time.Time) string {
	return fmt.Sprintf(`{"objectID":"%s","title":%q,"url":%s,"author":%q,"points":%d,"num_comments":%d,"created_at":%q,"created_at_i":%d}`,
		id, title, urlField, author, points, comments, created.Format("2006-01-02T15:04:05.000Z"), created.Unix())
}

func titles(r *types.SearchResponse) []string {
	var out []string
	for _, it := range r.Results {
		out = append(out, it.Title)
	}
	return out
}

func TestTechNewsDefaultFeedTable(t *testing.T) {
	want := []string{
		"https://feed.infoq.com/", "https://martinfowler.com/feed.atom", "https://newsletter.pragmaticengineer.com/feed",
		"https://blog.bytebytego.com/feed", "https://netflixtechblog.com/feed", "https://blog.cloudflare.com/rss/",
		"https://github.blog/engineering/feed/", "https://stackoverflow.blog/feed/", "https://aws.amazon.com/blogs/architecture/feed/",
		"https://developers.googleblog.com/feeds/posts/default", "https://thenewstack.io/feed/", "https://go.dev/blog/feed.atom",
		"https://feeds.arstechnica.com/arstechnica/technology-lab", "https://techcrunch.com/feed/", "https://www.theverge.com/rss/index.xml",
		"https://www.wired.com/feed/rss", "https://www.theregister.com/headlines.atom", "https://www.xataka.com/feedburner.xml",
		"https://www.genbeta.com/feedburner.xml",
	}
	feeds := DefaultTechFeeds()
	if len(feeds) != len(want) {
		t.Fatalf("feeds = %d, want %d", len(feeds), len(want))
	}
	names := map[string]bool{}
	for i, f := range feeds {
		if f.URL != want[i] || f.Name == "" || names[f.Name] {
			t.Errorf("feed %d = %+v (want url %s, unique non-empty name)", i, f, want[i])
		}
		names[f.Name] = true
	}
	if !names["infoq"] || !names["go blog"] {
		t.Errorf("expected short lower-case source names, got %v", names)
	}
}

func TestTechNewsMergesRanksAndDedupes(t *testing.T) {
	hn := hnJSON(
		hnHit("111", "Kubernetes 1.40 released", `"https://k8s.example/blog/1-40"`, "alice", 412, 130, techNow.Add(-5*time.Hour)),
		hnHit("222", "Ask HN: how do you run Kubernetes?", `null`, "bob", 10, 7, techNow.Add(-2*time.Hour)),
		hnHit("333", "<b>Kubernetes</b> &amp; friends", `"https://k8s.example/friends"`, "eve", 1, 0, techNow.Add(-30*time.Hour)),
	)
	gn := gnewsRSS(
		[3]string{"Kubernetes 1.40 released - Daily Planet", "https://news.google.example/1", "Daily Planet"},
		[3]string{"Kubernetes outage explained - The Wire", "https://news.google.example/2", "The Wire"},
	)
	infoq := rssAt(
		[3]string{"Kubernetes 1.40 released", "https://k8s.example/blog/1-40?utm_source=rss", ago(5 * time.Hour)},
		[3]string{"Docker news without the k word", "https://infoq.example/docker", ago(time.Hour)},
		[3]string{"Platform engineering with Kubernetes", "https://infoq.example/platform", ago(3 * time.Hour)},
	)
	f := newTechFixture(t, hn, 200, gn, 200, feedSpec{name: "infoq", body: infoq})
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes", MaxResults: 10})

	if resp.Partial || len(resp.Warnings) != 0 {
		t.Fatalf("healthy search: partial=%v warnings=%v", resp.Partial, resp.Warnings)
	}
	got := titles(resp)
	// Same URL / title seen twice is kept once; the unrelated Docker item is filtered out.
	if len(got) != 5 {
		t.Fatalf("results = %q", got)
	}
	for _, it := range resp.Results {
		if strings.Contains(it.Title, "Docker") {
			t.Errorf("curated item without the query term leaked: %q", it.Title)
		}
		if it.Type != "news" || it.PublishedAt == nil && it.Engine != "google news rss" {
			t.Errorf("type/date wrong: %+v", it)
		}
	}
	for i := 1; i < len(resp.Results); i++ {
		if !(resp.Results[i].Score < resp.Results[i-1].Score) {
			t.Fatalf("scores must strictly decrease: %+v", resp.Results)
		}
	}
	byEngine := map[string]int{}
	for _, it := range resp.Results {
		byEngine[it.Engine]++
	}
	if byEngine["hn algolia"] != 3 || byEngine["google news rss"] != 1 || byEngine["infoq"] != 1 {
		t.Errorf("engines = %v (titles %q)", byEngine, got)
	}
	// Ranking: same tier (every title has the term), so the sources take
	// turns and HN leads with its own first hit.
	if got[0] != "Kubernetes 1.40 released" || got[1] != "Kubernetes outage explained - The Wire" {
		t.Errorf("sources should interleave starting with HN's first hit, got %q", got)
	}
	var ask, release types.SearchResultItem
	for _, it := range resp.Results {
		if strings.HasPrefix(it.Title, "Ask HN") {
			ask = it
		}
		if it.Title == "Kubernetes 1.40 released" {
			release = it
		}
	}
	if ask.URL != "https://news.ycombinator.com/item?id=222" || ask.Author != "bob" || ask.Source != "hn algolia" {
		t.Errorf("Ask HN item must use the HN item URL: %+v", ask)
	}
	if !strings.Contains(ask.Snippet, "10 points") || !strings.Contains(ask.Snippet, "7 comments") {
		t.Errorf("snippet lacks points/comments: %q", ask.Snippet)
	}
	if release.URL != "https://k8s.example/blog/1-40" || !strings.Contains(release.Snippet, "412 points") || !strings.Contains(release.Snippet, "news.ycombinator.com/item?id=111") {
		t.Errorf("HN story = %+v", release)
	}
	for _, it := range resp.Results {
		if strings.ContainsAny(it.Title, "<>") || strings.Contains(it.Title, "&amp;") {
			t.Errorf("title not sanitized: %q", it.Title)
		}
	}
	q := f.hnQuery
	if q.Get("query") != "kubernetes" || q.Get("tags") != "story" {
		t.Errorf("HN query = %v", q)
	}
	if f.gnQuery.Get("q") != "kubernetes" {
		t.Errorf("Google News query = %v", f.gnQuery)
	}
}

func TestTechNewsFeedFilterIsAccentInsensitiveAllTermsThenFallsBackToAny(t *testing.T) {
	feed := rssAt(
		[3]string{"Análisis de Kubernetes en producción", "https://x.example/1", ago(2 * time.Hour)},
		[3]string{"Kubernetes sin más", "https://x.example/2", ago(time.Hour)},
		[3]string{"Cocina de verano", "https://x.example/3", ago(time.Hour)},
	)
	f := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200, feedSpec{name: "xataka", body: feed})
	resp := f.search(t, &types.SearchRequest{Query: "analisis kubernetes"})
	if got := titles(resp); len(got) != 1 || got[0] != "Análisis de Kubernetes en producción" {
		t.Fatalf("all-terms accent-insensitive match failed: %q", got)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("warnings = %v", resp.Warnings)
	}

	resp = f.search(t, &types.SearchRequest{Query: "kubernetes terraform"})
	got := titles(resp)
	if len(got) != 2 || got[0] != "Kubernetes sin más" {
		t.Fatalf("fallback to any term, newest first: %q", got)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "any") {
		t.Errorf("fallback must be announced: %v", resp.Warnings)
	}
}

func TestTechNewsStopwordOnlyQueryUsesAllTokens(t *testing.T) {
	f := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200, feedSpec{name: "infoq", body: rssAt([3]string{"The future of the web", "https://x.example/1", ago(time.Hour)})})
	resp := f.search(t, &types.SearchRequest{Query: "the"})
	if len(resp.Results) != 1 {
		t.Fatalf("results = %q warnings=%v", titles(resp), resp.Warnings)
	}
}

func TestTechNewsTimeRangeFiltersByDateAndDropsUndated(t *testing.T) {
	feed := rssAt(
		[3]string{"Kubernetes today", "https://x.example/today", ago(3 * time.Hour)},
		[3]string{"Kubernetes last month", "https://x.example/old", ago(40 * 24 * time.Hour)},
		[3]string{"Kubernetes undated", "https://x.example/undated", ""},
	)
	hn := hnJSON(
		hnHit("1", "Kubernetes HN fresh", `"https://hn.example/fresh"`, "a", 1, 1, techNow.Add(-2*time.Hour)),
		hnHit("2", "Kubernetes HN stale", `"https://hn.example/stale"`, "a", 1, 1, techNow.Add(-72*time.Hour)),
	)
	f := newTechFixture(t, hn, 200, gnewsRSS(), 200, feedSpec{name: "infoq", body: feed})
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes", TimeRange: "day"})
	got := titles(resp)
	if len(got) != 2 {
		t.Fatalf("day filter: %q", got)
	}
	for _, g := range got {
		if g != "Kubernetes today" && g != "Kubernetes HN fresh" {
			t.Errorf("unexpected item %q", g)
		}
	}
	if f.gnQuery.Get("q") != "kubernetes when:1d" {
		t.Errorf("Google query = %v", f.gnQuery)
	}
	if nf := f.hnQuery.Get("numericFilters"); nf != fmt.Sprintf("created_at_i>%d", techNow.Add(-24*time.Hour).Unix()) {
		t.Errorf("HN numericFilters = %q", nf)
	}

	resp = f.search(t, &types.SearchRequest{Query: "kubernetes", TimeRange: "year"})
	if got := titles(resp); len(got) != 4 {
		t.Errorf("year filter keeps dated items only: %q", got)
	}
	resp = f.search(t, &types.SearchRequest{Query: "kubernetes"})
	if got := titles(resp); len(got) != 5 {
		t.Errorf("no timeRange keeps undated items: %q", got)
	}
}

func TestTechNewsFailedFeedsAreOneSummarizedWarning(t *testing.T) {
	good := rssAt([3]string{"Kubernetes good", "https://x.example/good", ago(time.Hour)})
	f := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200,
		feedSpec{name: "good", body: good},
		feedSpec{name: "broken-a", status: 500},
		feedSpec{name: "broken-b", body: "<html>not a feed</html>"},
	)
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes"})
	if len(resp.Results) != 1 || resp.Partial {
		t.Fatalf("one feed worked and the query sources answered: partial=%v results=%q", resp.Partial, titles(resp))
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0] != "tech news: 2 of 5 sources failed: broken-a, broken-b" {
		t.Errorf("warnings = %v", resp.Warnings)
	}
}

func TestTechNewsPartialOnlyWhenAllQuerySourcesFail(t *testing.T) {
	good := rssAt([3]string{"Kubernetes good", "https://x.example/good", ago(time.Hour)})
	f := newTechFixture(t, "", 500, "", 503, feedSpec{name: "good", body: good})
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes"})
	if !resp.Partial || len(resp.Results) != 1 {
		t.Fatalf("partial=%v results=%q", resp.Partial, titles(resp))
	}
	joined := strings.Join(resp.Warnings, " | ")
	if !strings.Contains(joined, "hn algolia") || !strings.Contains(joined, "google news rss") {
		t.Errorf("warnings must name the failed query sources: %v", resp.Warnings)
	}

	f2 := newTechFixture(t, "", 500, gnewsRSS([3]string{"Kubernetes via Google - P", "https://news.google.example/k", "P"}), 200)
	resp = f2.search(t, &types.SearchRequest{Query: "kubernetes"})
	if resp.Partial || len(resp.Results) != 1 {
		t.Errorf("one query source up means not partial: partial=%v results=%q warnings=%v", resp.Partial, titles(resp), resp.Warnings)
	}
}

func TestTechNewsEverySourceDownIsPartialAndEmpty(t *testing.T) {
	f := newTechFixture(t, "", 500, "", 500, feedSpec{name: "a", status: 500})
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes"})
	if !resp.Partial || len(resp.Results) != 0 || len(resp.Warnings) == 0 {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestTechNewsCachesFeedsAndResponses(t *testing.T) {
	feed := rssAt([3]string{"Kubernetes good", "https://x.example/good", ago(time.Hour)}, [3]string{"Docker good", "https://x.example/docker", ago(time.Hour)})
	f := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200, feedSpec{name: "infoq", body: feed})
	c := NewTechNewsConnector(cache.NewService(300), f.cfg)
	ctx := context.Background()
	if _, err := c.Search(ctx, &types.SearchRequest{Query: "kubernetes"}); err != nil {
		t.Fatal(err)
	}
	resp, _ := c.Search(ctx, &types.SearchRequest{Query: "kubernetes"})
	if !resp.Cached || f.hnHits.Load() != 1 {
		t.Fatalf("same query must be served from cache: cached=%v hn=%d", resp.Cached, f.hnHits.Load())
	}
	resp, _ = c.Search(ctx, &types.SearchRequest{Query: "docker"})
	if f.feedHits.Load() != 1 {
		t.Errorf("a different query must reuse the cached feed, feed hits = %d", f.feedHits.Load())
	}
	if f.hnHits.Load() != 2 || len(resp.Results) != 1 || resp.Results[0].Title != "Docker good" {
		t.Errorf("hn hits=%d results=%q", f.hnHits.Load(), titles(resp))
	}
}

func TestTechNewsFeedCacheKeepsFeedsForThirtyMinutes(t *testing.T) {
	if techFeedCacheTTL != 30*time.Minute {
		t.Fatalf("techFeedCacheTTL = %v", techFeedCacheTTL)
	}
	c := NewTechNewsConnector(cache.NewService(300), TechNewsConfig{})
	keys, _ := c.feedCache.Keys(context.Background())
	if len(keys) != 0 {
		t.Fatalf("keys = %v", keys)
	}
	c.storeFeed(context.Background(), "https://x.example/feed", []feedItem{{Title: "t", Link: "https://x.example/1"}})
	keys, _ = c.feedCache.Keys(context.Background())
	if len(keys) != 1 {
		t.Fatalf("keys = %v", keys)
	}
	entry, ok, _ := c.feedCache.Get(context.Background(), keys[0])
	if !ok {
		t.Fatal("feed not stored")
	}
	if d := entry.ExpiresAt.Sub(entry.CreatedAt); d != 30*time.Minute {
		t.Errorf("feed TTL = %v, want 30m", d)
	}
}

func TestTechNewsBoundsFeedConcurrency(t *testing.T) {
	var inflight, peak atomic.Int32
	var servers []*httptest.Server
	cfg := TechNewsConfig{Workers: 3, Now: func() time.Time { return techNow }}
	for i := 0; i < 10; i++ {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			n := inflight.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(30 * time.Millisecond)
			inflight.Add(-1)
			_, _ = w.Write([]byte(rssAt([3]string{fmt.Sprintf("Kubernetes item %d", i), "https://x.example/" + fmt.Sprint(i), ago(time.Hour)})))
		}))
		servers = append(servers, srv)
		cfg.Feeds = append(cfg.Feeds, TechFeed{Name: fmt.Sprintf("f%d", i), URL: srv.URL})
	}
	defer func() {
		for _, s := range servers {
			s.Close()
		}
	}()
	hn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(emptyHN)) }))
	defer hn.Close()
	gn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(gnewsRSS())) }))
	defer gn.Close()
	cfg.HNURL, cfg.GoogleNewsURL = hn.URL, gn.URL
	resp, err := NewTechNewsConnector(cache.NewService(300), cfg).Search(context.Background(), &types.SearchRequest{Query: "kubernetes"})
	if err != nil || len(resp.Results) != 10 {
		t.Fatalf("resp=%d err=%v warnings=%v", len(resp.Results), err, resp.Warnings)
	}
	if p := peak.Load(); p > 3 || p < 2 {
		t.Errorf("peak concurrent feed requests = %d, want 2..3", p)
	}
}

func TestTechNewsOverallDeadlineAndPerFeedTimeout(t *testing.T) {
	good := rssAt([3]string{"Kubernetes good", "https://x.example/good", ago(time.Hour)})
	f := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200,
		feedSpec{name: "good", body: good},
		feedSpec{name: "hangs", body: good, delay: 5 * time.Second},
	)
	f.cfg.FeedTimeout = 100 * time.Millisecond
	f.cfg.Deadline = 2 * time.Second
	start := time.Now()
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes"})
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("a hung feed must be cut at the per-feed timeout, search took %v", elapsed)
	}
	if len(resp.Results) != 1 || len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "hangs") {
		t.Errorf("results=%q warnings=%v", titles(resp), resp.Warnings)
	}

	// The overall deadline also bounds feeds that were still queued.
	g := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200,
		feedSpec{name: "slow1", body: good, delay: 5 * time.Second},
		feedSpec{name: "slow2", body: good, delay: 5 * time.Second},
		feedSpec{name: "queued", body: good},
	)
	g.cfg.Workers = 1
	g.cfg.FeedTimeout = 5 * time.Second
	g.cfg.Deadline = 300 * time.Millisecond
	start = time.Now()
	resp = g.search(t, &types.SearchRequest{Query: "kubernetes"})
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Fatalf("overall deadline not honored: %v", elapsed)
	}
	if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "3 of 5 sources failed") {
		t.Errorf("warnings = %v", resp.Warnings)
	}
}

func TestTechNewsCapsResultsAndKeepsScoresDecreasing(t *testing.T) {
	var items [][3]string
	for i := 0; i < 80; i++ {
		items = append(items, [3]string{fmt.Sprintf("Kubernetes story %d", i), fmt.Sprintf("https://x.example/%d", i), ago(time.Duration(i+1) * time.Hour)})
	}
	f := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200, feedSpec{name: "infoq", body: rssAt(items...)})
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes", MaxResults: 5})
	if len(resp.Results) != maxTechNewsResults {
		t.Fatalf("results = %d, want cap %d", len(resp.Results), maxTechNewsResults)
	}
	if resp.Results[0].Title != "Kubernetes story 0" || resp.Results[0].Score <= resp.Results[1].Score {
		t.Errorf("ranking/scores: %+v %+v", resp.Results[0], resp.Results[1])
	}
	raw, _ := json.Marshal(resp)
	if !strings.Contains(string(raw), `"sourcesUsed":["technews"]`) {
		t.Errorf("sourcesUsed = %v", resp.SourcesUsed)
	}
}

// A source that returns many fresh items must not push the others out of the
// capped list: Google News items are newer than HN stories but each query
// source contributes at most maxTechPerQuerySource items.
func TestTechNewsOneQuerySourceCannotFillTheWholeList(t *testing.T) {
	var gn []string
	for i := 0; i < 60; i++ {
		gn = append(gn, fmt.Sprintf(`<item><title>Kubernetes news %d - Pub</title><link>https://news.google.com/rss/articles/%d</link><pubDate>%s</pubDate><source url="https://pub.example">Pub</source></item>`, i, i, ago(time.Duration(i+1)*time.Minute)))
	}
	var hits []string
	for i := 0; i < 10; i++ {
		hits = append(hits, hnHit(fmt.Sprintf("%d", 100+i), fmt.Sprintf("Kubernetes HN story %d", i), `"https://hn.example/`+fmt.Sprint(i)+`"`, "pg", 100, 10, techNow.Add(-time.Duration(i+10)*24*time.Hour)))
	}
	f := newTechFixture(t, hnJSON(hits...), 200, `<rss><channel>`+strings.Join(gn, "")+`</channel></rss>`, 200)
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes"})
	hn, g := 0, 0
	for _, r := range resp.Results {
		switch r.Engine {
		case "hn algolia":
			hn++
		case "google news rss":
			g++
		}
	}
	if hn != 10 || g != maxTechPerQuerySource {
		t.Fatalf("hn=%d google=%d (cap %d), results=%d", hn, g, maxTechPerQuerySource, len(resp.Results))
	}
}

// Probed live: Google News returns nothing for `when:1m`; `when:30d` works.
func TestTechNewsMonthAsksGoogleNewsForThirtyDays(t *testing.T) {
	f := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200)
	f.search(t, &types.SearchRequest{Query: "arquitectura de software", Language: "es", TimeRange: "month"})
	if q := f.gnQuery.Get("q"); q != "arquitectura de software when:30d" {
		t.Errorf("Google News q = %q", q)
	}
}

// HN's relevance ranking buries recent stories under old high-score ones, so
// the request always carries a created_at_i floor: the timeRange window, or
// one year when no timeRange is given.
func TestTechNewsHNAlwaysGetsARecencyFloor(t *testing.T) {
	cases := []struct {
		timeRange string
		window    time.Duration
	}{
		{"", 365 * 24 * time.Hour},
		{"day", 24 * time.Hour},
		{"week", 7 * 24 * time.Hour},
		{"month", 30 * 24 * time.Hour},
		{"year", 365 * 24 * time.Hour},
	}
	for _, tc := range cases {
		f := newTechFixture(t, emptyHN, 200, gnewsRSS(), 200)
		f.search(t, &types.SearchRequest{Query: "kubernetes", TimeRange: tc.timeRange})
		want := fmt.Sprintf("created_at_i>%d", techNow.Add(-tc.window).Unix())
		if got := f.hnQuery.Get("numericFilters"); got != want {
			t.Errorf("timeRange %q: numericFilters = %q, want %q", tc.timeRange, got, want)
		}
	}
}

// Within the same match tier the sources take turns, each keeping its own
// order (HN by its relevance, the others newest first), so the newest source
// cannot fill the page.
func TestTechNewsInterleavesSourcesWithinATier(t *testing.T) {
	var hits []string
	for i := 0; i < 3; i++ {
		hits = append(hits, hnHit(fmt.Sprintf("%d", 100+i), fmt.Sprintf("Kubernetes hn %d", i), fmt.Sprintf(`"https://hn.example/%d"`, i), "pg", 300-i, 5, techNow.Add(-time.Duration(10+i)*24*time.Hour)))
	}
	var gn []string
	for i := 0; i < 5; i++ {
		gn = append(gn, fmt.Sprintf(`<item><title>Kubernetes gn %d - Pub</title><link>https://news.google.com/rss/articles/%d</link><pubDate>%s</pubDate></item>`, i, i, ago(time.Duration(i+1)*time.Minute)))
	}
	feed := rssAt(
		[3]string{"Kubernetes feed 1", "https://feed.example/1", ago(3 * time.Hour)},
		[3]string{"Kubernetes feed 0", "https://feed.example/0", ago(2 * time.Hour)},
		[3]string{"Kubernetes feed 2", "https://feed.example/2", ago(4 * time.Hour)},
	)
	f := newTechFixture(t, hnJSON(hits...), 200, `<rss><channel>`+strings.Join(gn, "")+`</channel></rss>`, 200, feedSpec{name: "infoq", body: feed})
	resp := f.search(t, &types.SearchRequest{Query: "kubernetes"})
	want := []string{
		"Kubernetes hn 0", "Kubernetes gn 0 - Pub", "Kubernetes feed 0",
		"Kubernetes hn 1", "Kubernetes gn 1 - Pub", "Kubernetes feed 1",
		"Kubernetes hn 2", "Kubernetes gn 2 - Pub", "Kubernetes feed 2",
		"Kubernetes gn 3 - Pub", "Kubernetes gn 4 - Pub",
	}
	got := titles(resp)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("order\n got %q\nwant %q", got, want)
	}
	for i := 1; i < len(resp.Results); i++ {
		if !(resp.Results[i].Score < resp.Results[i-1].Score) {
			t.Fatalf("scores must strictly decrease: %+v", resp.Results)
		}
	}
}

// A stronger match tier still outranks the interleaving of a weaker one.
func TestTechNewsStrongerTierBeatsInterleaving(t *testing.T) {
	hn := hnJSON(hnHit("1", "Cluster ops", `"https://hn.example/1"`, "pg", 900, 5, techNow.Add(-24*time.Hour)))
	feed := rssAt([3]string{"Kubernetes in the title", "https://feed.example/1", ago(48 * time.Hour)})
	f := newTechFixture(t, hn, 200, gnewsRSS(), 200, feedSpec{name: "infoq", body: feed})
	got := titles(f.search(t, &types.SearchRequest{Query: "kubernetes"}))
	if len(got) != 2 || got[0] != "Kubernetes in the title" {
		t.Fatalf("got %q", got)
	}
}
