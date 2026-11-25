package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// search_tech_news: technology and software engineering news, keyless. Two
// query sources (HN Algolia search and Google News RSS search) plus a built-in
// list of curated RSS/Atom feeds that is fetched concurrently, cached per feed
// and filtered by the query terms.

const (
	techNewsName = "technews"
	hnEngine     = "hn algolia"

	defaultHNURL = "https://hn.algolia.com/api/v1/search"
	hnItemURL    = "https://news.ycombinator.com/item?id="
	hnHitsPage   = 30
	// hnDefaultWindow bounds HN stories when the caller gives no timeRange.
	hnDefaultWindow = 365 * 24 * time.Hour

	// techFeedCacheTTL is how long a parsed curated feed is reused.
	techFeedCacheTTL = 30 * time.Minute
	// maxTechNewsResults caps the ranked list one search returns (the MCP
	// layer pages it with cursors).
	maxTechNewsResults = 50
	// maxTechPerQuerySource caps what HN or Google News adds, so fresh Google
	// News items cannot push every other source out of the ranked list.
	maxTechPerQuerySource = 20
	// maxTechSnippetRunes caps a composed HN snippet.
	maxTechSnippetRunes = 400

	defaultTechWorkers     = 6
	defaultTechFeedTimeout = 8 * time.Second
	defaultTechDeadline    = 12 * time.Second
)

// TechFeed is one curated feed: Name is the short lower-case label used as
// the result Engine and Source.
type TechFeed struct {
	Name string
	URL  string
}

// techNewsFeeds is the built-in curated list (all answered 200 from the
// production host, see odd/tasks/searxng-capabilities.md L21).
var techNewsFeeds = []TechFeed{
	{"infoq", "https://feed.infoq.com/"},
	{"martin fowler", "https://martinfowler.com/feed.atom"},
	{"the pragmatic engineer", "https://newsletter.pragmaticengineer.com/feed"},
	{"bytebytego", "https://blog.bytebytego.com/feed"},
	{"netflix techblog", "https://netflixtechblog.com/feed"},
	{"cloudflare blog", "https://blog.cloudflare.com/rss/"},
	{"github engineering", "https://github.blog/engineering/feed/"},
	{"stack overflow blog", "https://stackoverflow.blog/feed/"},
	{"aws architecture", "https://aws.amazon.com/blogs/architecture/feed/"},
	{"google developers", "https://developers.googleblog.com/feeds/posts/default"},
	{"the new stack", "https://thenewstack.io/feed/"},
	{"go blog", "https://go.dev/blog/feed.atom"},
	{"ars technica", "https://feeds.arstechnica.com/arstechnica/technology-lab"},
	{"techcrunch", "https://techcrunch.com/feed/"},
	{"the verge", "https://www.theverge.com/rss/index.xml"},
	{"wired", "https://www.wired.com/feed/rss"},
	{"the register", "https://www.theregister.com/headlines.atom"},
	{"xataka", "https://www.xataka.com/feedburner.xml"},
	{"genbeta", "https://www.genbeta.com/feedburner.xml"},
}

// DefaultTechFeeds returns a copy of the built-in curated feed list.
func DefaultTechFeeds() []TechFeed { return slices.Clone(techNewsFeeds) }

// TechNewsConfig tunes the connector; the zero value gives production
// defaults. The URL and timing fields exist so tests can run against local
// servers.
type TechNewsConfig struct {
	HNURL         string        // HN Algolia search endpoint
	GoogleNewsURL string        // Google News RSS search endpoint
	Feeds         []TechFeed    // curated feeds (default: DefaultTechFeeds)
	Workers       int           // concurrent curated feed fetches (default 6)
	FeedTimeout   time.Duration // per curated feed (default 8s)
	Deadline      time.Duration // whole search (default 12s)
	Now           func() time.Time
}

type TechNewsConnector struct {
	cacheSvc  *cache.Service
	feedCache *cache.Service
	client    *http.Client

	hnURL, gnURL string
	feeds        []TechFeed
	workers      int
	feedTimeout  time.Duration
	deadline     time.Duration
	now          func() time.Time
}

func NewTechNewsConnector(cacheSvc *cache.Service, cfg TechNewsConfig) *TechNewsConnector {
	c := &TechNewsConnector{
		cacheSvc: cacheSvc,
		// Curated feeds live in their own cache instance so they keep the
		// 30 minute lifetime whatever the global search TTL is.
		feedCache:   cache.NewService(int(techFeedCacheTTL.Seconds())),
		client:      &http.Client{},
		hnURL:       cfg.HNURL,
		gnURL:       cfg.GoogleNewsURL,
		feeds:       cfg.Feeds,
		workers:     cfg.Workers,
		feedTimeout: cfg.FeedTimeout,
		deadline:    cfg.Deadline,
		now:         cfg.Now,
	}
	if c.hnURL == "" {
		c.hnURL = defaultHNURL
	}
	if c.gnURL == "" {
		c.gnURL = googleNewsRSSURL
	}
	if c.feeds == nil {
		c.feeds = DefaultTechFeeds()
	}
	if c.workers <= 0 {
		c.workers = defaultTechWorkers
	}
	if c.feedTimeout <= 0 {
		c.feedTimeout = defaultTechFeedTimeout
	}
	if c.deadline <= 0 {
		c.deadline = defaultTechDeadline
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

func (c *TechNewsConnector) Name() string { return techNewsName }

// techCandidate is one item from any source before filtering and ranking.
type techCandidate struct {
	res       types.SearchResultItem // everything but Score and CitationID
	summary   string                 // extra text matched against the query
	published time.Time
	trusted   bool // came from a query source: already relevant upstream
}

// techSource is the outcome of one source.
type techSource struct {
	name  string
	query bool // HN or Google News (a query source), not a curated feed
	cands []techCandidate
	err   error
}

func (c *TechNewsConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateSearchCacheKey(query, []string{techNewsName}, req, maxResults)
	if c.cacheSvc != nil {
		if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
			log.Printf("[technews] cache hit for query: %s", query)
			cachedResp := &types.SearchResponse{}
			if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
				cachedResp.Cached = true
				return cachedResp, nil
			}
		}
	}

	ctx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()
	now := c.now()
	sources := c.gather(ctx, query, req, now)

	resp := &types.SearchResponse{
		Query:       req.Query,
		SourcesUsed: []string{techNewsName},
	}
	results, warnings := c.assemble(query, req.TimeRange, now, sources)
	resp.Results = results

	var failed []string
	queryTotal, queryFailed := 0, 0
	for _, s := range sources {
		if s.query {
			queryTotal++
		}
		if s.err == nil {
			continue
		}
		log.Printf("[technews] source %s failed: %v", s.name, s.err)
		failed = append(failed, s.name)
		if s.query {
			queryFailed++
		}
	}
	if len(failed) > 0 {
		resp.Warnings = append(resp.Warnings, fmt.Sprintf("tech news: %d of %d sources failed: %s", len(failed), len(sources), strings.Join(failed, ", ")))
	}
	resp.Warnings = append(resp.Warnings, warnings...)
	if queryTotal > 0 && queryFailed == queryTotal {
		observability.Default().RecordSearchDegraded(techNewsName, "query_sources_failed")
		resp.Partial = true
	}

	cacheSearchResponse(ctx, c.cacheSvc, cacheKey, resp, len(failed) > 0)
	log.Printf("[technews] search completed: query=%s, results=%d, failed=%d, latency=%v", query, len(results), len(failed), time.Since(start))
	return resp, nil
}

// gather queries HN and Google News and loads every curated feed through a
// bounded worker pool. Each goroutine writes only its own slot.
func (c *TechNewsConnector) gather(ctx context.Context, query string, req *types.SearchRequest, now time.Time) []techSource {
	sources := make([]techSource, 2+len(c.feeds))
	sources[0] = techSource{name: hnEngine, query: true}
	sources[1] = techSource{name: googleNewsEngine, query: true}
	for i, f := range c.feeds {
		sources[2+i] = techSource{name: f.Name}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sources[0].cands, sources[0].err = c.searchHN(ctx, query, req.TimeRange, now)
	}()
	go func() {
		defer wg.Done()
		sources[1].cands, sources[1].err = c.searchGoogleNews(ctx, query, req)
	}()

	jobs := make(chan int)
	workers := min(c.workers, len(c.feeds))
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				sources[2+i].cands, sources[2+i].err = c.loadFeed(ctx, c.feeds[i])
			}
		}()
	}
	for i := range c.feeds {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return sources
}

// assemble filters, ranks, deduplicates and scores the candidates of every
// source. The returned warnings are the filter notices (never source
// failures).
func (c *TechNewsConnector) assemble(query, timeRange string, now time.Time, sources []techSource) ([]types.SearchResultItem, []string) {
	cutoff, hasCutoff := techCutoff(timeRange, now)
	terms := techQueryTerms(query)

	type scored struct {
		cand     techCandidate
		query    bool
		source   int // index in sources
		order    int // position inside the source's own list
		strength float64
		matched  int
	}
	var queryItems, curated []scored
	for si, s := range sources {
		for ci, cand := range s.cands {
			if hasCutoff && (cand.published.IsZero() || cand.published.Before(cutoff)) {
				continue
			}
			matched, inTitle := techMatch(terms, cand.res.Title, cand.summary)
			sc := scored{cand: cand, matched: matched, query: s.query, source: si, order: ci}
			if len(terms) > 0 {
				ratio := float64(matched) / float64(len(terms))
				if cand.trusted {
					ratio = 1
				}
				sc.strength = ratio + 0.25*float64(inTitle)/float64(len(terms))
			}
			if s.query {
				queryItems = append(queryItems, sc)
			} else {
				curated = append(curated, sc)
			}
		}
	}

	var warnings []string
	var kept []scored
	for _, sc := range curated {
		if len(terms) == 0 || sc.matched == len(terms) {
			kept = append(kept, sc)
		}
	}
	if len(kept) == 0 && len(queryItems) == 0 && len(terms) > 1 {
		for _, sc := range curated {
			if sc.matched > 0 {
				kept = append(kept, sc)
			}
		}
		if len(kept) > 0 {
			warnings = append(warnings, "tech news: no item matched all query terms; showing items that match any of them")
		}
	}

	all := interleaveTiers(append(queryItems, kept...), func(sc scored) (float64, int, int, time.Time, bool) {
		return sc.strength, sc.source, sc.order, sc.cand.published, sc.cand.trusted && sc.cand.res.Engine == hnEngine
	})

	dedupe := newFeedDedupe()
	var chosen []types.SearchResultItem
	perQuerySource := map[string]int{}
	for _, sc := range all {
		if len(chosen) >= maxTechNewsResults {
			break
		}
		r := sc.cand.res
		if sc.query && perQuerySource[r.Engine] >= maxTechPerQuerySource {
			continue
		}
		if !dedupe.add(r.URL, r.Title, r.Source) {
			continue
		}
		if sc.query {
			perQuerySource[r.Engine]++
		}
		chosen = append(chosen, r)
	}
	n := len(chosen)
	for i := range chosen {
		domain := "unknown"
		if u, err := url.Parse(chosen[i].URL); err == nil && u.Hostname() != "" {
			domain = u.Hostname()
		}
		chosen[i].Score = float64(n - i)
		chosen[i].CitationID = fmt.Sprintf("%s:%s:%d", techNewsName, domain, i)
	}
	if chosen == nil {
		chosen = []types.SearchResultItem{}
	}
	return chosen, warnings
}

// techCutoff is the oldest accepted publication time for a timeRange.
func techCutoff(timeRange string, now time.Time) (time.Time, bool) {
	switch timeRange {
	case "day":
		return now.Add(-24 * time.Hour), true
	case "week":
		return now.Add(-7 * 24 * time.Hour), true
	case "month":
		return now.Add(-30 * 24 * time.Hour), true
	case "year":
		return now.Add(-365 * 24 * time.Hour), true
	}
	return time.Time{}, false
}

var techStopwords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "from": true, "about": true, "what": true, "how": true,
	"new": true, "news": true, "latest": true, "are": true, "was": true, "is": true, "of": true, "in": true,
	"on": true, "to": true, "a": true, "an": true,
	"el": true, "la": true, "los": true, "las": true, "un": true, "una": true, "de": true, "del": true,
	"en": true, "y": true, "que": true, "con": true, "por": true, "para": true, "sobre": true, "noticias": true,
}

// techQueryTerms are the significant, accent-folded words of the query: short
// words and stopwords are dropped unless nothing else is left.
func techQueryTerms(query string) []string {
	tokens := feedTokens(query)
	var terms []string
	for _, t := range tokens {
		if len([]rune(t)) >= 2 && !techStopwords[t] && !slices.Contains(terms, t) {
			terms = append(terms, t)
		}
	}
	if len(terms) > 0 {
		return terms
	}
	for _, t := range tokens {
		if !slices.Contains(terms, t) {
			terms = append(terms, t)
		}
	}
	return terms
}

// techMatch counts the query terms found in title+summary and, separately, in
// the title. A term matches a word that equals it, or that starts with it when
// the term has at least 4 letters (container -> containers).
func techMatch(terms []string, title, summary string) (matched, inTitle int) {
	titleTokens := feedTokens(title)
	allTokens := append(slices.Clone(titleTokens), feedTokens(summary)...)
	has := func(tokens []string, term string) bool {
		for _, tok := range tokens {
			if tok == term || (len([]rune(term)) >= 4 && strings.HasPrefix(tok, term)) {
				return true
			}
		}
		return false
	}
	for _, term := range terms {
		if has(allTokens, term) {
			matched++
		}
		if has(titleTokens, term) {
			inTitle++
		}
	}
	return matched, inTitle
}

type hnResponse struct {
	Hits []struct {
		ObjectID    string  `json:"objectID"`
		Title       string  `json:"title"`
		URL         *string `json:"url"`
		Author      string  `json:"author"`
		Points      int     `json:"points"`
		NumComments int     `json:"num_comments"`
		CreatedAt   string  `json:"created_at"`
		CreatedAtI  int64   `json:"created_at_i"`
		StoryText   string  `json:"story_text"`
	} `json:"hits"`
}

// searchHN queries HN Algolia for stories. Stories without an external URL
// (Ask HN) point at the HN item page.
func (c *TechNewsConnector) searchHN(ctx context.Context, query, timeRange string, now time.Time) ([]techCandidate, error) {
	ctx, cancel := context.WithTimeout(ctx, c.feedTimeout)
	defer cancel()
	params := url.Values{}
	params.Set("query", query)
	params.Set("tags", "story")
	params.Set("hitsPerPage", strconv.Itoa(hnHitsPage))
	// HN ranks by relevance and points, so without a floor the top hits are
	// years old and lose against fresher sources: always bound by date.
	cutoff, ok := techCutoff(timeRange, now)
	if !ok {
		cutoff = now.Add(-hnDefaultWindow)
	}
	params.Set("numericFilters", "created_at_i>"+strconv.FormatInt(cutoff.Unix(), 10))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.hnURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("User-Agent", feedUserAgent)
	httpReq.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var body hnResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxFeedBodyBytes)).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}
	var out []techCandidate
	for _, h := range body.Hits {
		title := sanitizeFeedText(h.Title, maxFeedTitleRunes)
		id := strings.TrimSpace(h.ObjectID)
		if title == "" || id == "" || strings.ContainsAny(id, "/?&# ") {
			continue
		}
		discussion := hnItemURL + id
		link, external := discussion, false
		if h.URL != nil {
			if u := strings.TrimSpace(*h.URL); len(u) <= maxFeedLinkBytes && isHTTPURL(u) {
				link, external = u, true
			}
		}
		parts := []string{fmt.Sprintf("%d points", h.Points), fmt.Sprintf("%d comments", h.NumComments)}
		if external {
			parts = append(parts, "discussion: "+discussion)
		}
		text := sanitizeFeedText(h.StoryText, maxFeedSnippetRunes)
		if text != "" {
			parts = append(parts, text)
		}
		var published time.Time
		if h.CreatedAtI > 0 {
			published = time.Unix(h.CreatedAtI, 0).UTC()
		} else {
			published = parseFeedDate(h.CreatedAt)
		}
		res := types.SearchResultItem{
			Title:   title,
			URL:     link,
			Snippet: truncateRunes(strings.Join(parts, " · "), maxTechSnippetRunes),
			Source:  hnEngine,
			Engine:  hnEngine,
			Type:    "news",
			Author:  sanitizeFeedText(h.Author, maxFeedNameRunes),
		}
		if !published.IsZero() {
			p := published
			res.PublishedAt = &p
		}
		out = append(out, techCandidate{res: res, summary: text, published: published, trusted: true})
	}
	return out, nil
}

// truncateRunes cuts s to max runes with an ellipsis.
func truncateRunes(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return strings.TrimSpace(string(r[:max])) + "..."
	}
	return s
}

func (c *TechNewsConnector) searchGoogleNews(ctx context.Context, query string, req *types.SearchRequest) ([]techCandidate, error) {
	ctx, cancel := context.WithTimeout(ctx, c.feedTimeout)
	defer cancel()
	items, err := fetchFeed(ctx, c.client, googleNewsSearchURL(c.gnURL, query, req.Language, req.TimeRange))
	if err != nil {
		return nil, err
	}
	out := make([]techCandidate, 0, len(items))
	for _, item := range items {
		res := googleNewsResult(item, "news", techNewsName, 0, 0)
		out = append(out, techCandidate{res: res, summary: item.Summary, published: item.Published, trusted: true})
	}
	return out, nil
}

// loadFeed returns the parsed items of a curated feed, from the 30 minute
// cache when present. A failed fetch is not cached.
func (c *TechNewsConnector) loadFeed(ctx context.Context, f TechFeed) ([]techCandidate, error) {
	items, ok := c.cachedFeed(ctx, f.URL)
	if !ok {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fctx, cancel := context.WithTimeout(ctx, c.feedTimeout)
		defer cancel()
		var err error
		if items, err = fetchFeed(fctx, c.client, f.URL); err != nil {
			return nil, err
		}
		c.storeFeed(ctx, f.URL, items)
	}
	out := make([]techCandidate, 0, len(items))
	for _, item := range items {
		res := types.SearchResultItem{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Summary,
			Source:  f.Name,
			Engine:  f.Name,
			Type:    "news",
			Author:  item.Author,
		}
		if !item.Published.IsZero() {
			p := item.Published
			res.PublishedAt = &p
		}
		out = append(out, techCandidate{res: res, summary: item.Summary, published: item.Published})
	}
	return out, nil
}

func feedCacheKey(feedURL string) string { return "technews:feed:v1:" + feedURL }

func (c *TechNewsConnector) cachedFeed(ctx context.Context, feedURL string) ([]feedItem, bool) {
	entry, ok, _ := c.feedCache.Get(ctx, feedCacheKey(feedURL))
	if !ok {
		return nil, false
	}
	var items []feedItem
	if err := json.Unmarshal(entry.Payload, &items); err != nil {
		return nil, false
	}
	return items, true
}

func (c *TechNewsConnector) storeFeed(ctx context.Context, feedURL string, items []feedItem) {
	data, err := json.Marshal(items)
	if err != nil {
		return
	}
	c.feedCache.SetWithMaxTTL(ctx, feedCacheKey(feedURL), data, []string{techNewsName}, techFeedCacheTTL)
}

// interleaveTiers orders items by descending match strength; inside one tier
// the sources take turns (in source order), each keeping its own order: HN
// keeps the relevance order it came in, every other source goes newest first
// (undated last). A single source can therefore not fill the page.
func interleaveTiers[T any](items []T, key func(T) (strength float64, source, order int, published time.Time, keepOrder bool)) []T {
	type lane struct {
		source int
		items  []T
	}
	byTier := map[float64]map[int][]T{}
	var tiers []float64
	for _, it := range items {
		strength, source, _, _, _ := key(it)
		lanes, ok := byTier[strength]
		if !ok {
			lanes = map[int][]T{}
			byTier[strength] = lanes
			tiers = append(tiers, strength)
		}
		lanes[source] = append(lanes[source], it)
	}
	sort.Float64s(tiers)
	slices.Reverse(tiers)

	out := make([]T, 0, len(items))
	for _, strength := range tiers {
		var lanes []lane
		for source, list := range byTier[strength] {
			_, _, _, _, keepOrder := key(list[0])
			if !keepOrder {
				sort.SliceStable(list, func(i, j int) bool {
					_, _, oi, pi, _ := key(list[i])
					_, _, oj, pj, _ := key(list[j])
					if pi.IsZero() != pj.IsZero() {
						return !pi.IsZero()
					}
					if !pi.Equal(pj) {
						return pi.After(pj)
					}
					return oi < oj
				})
			} else {
				sort.SliceStable(list, func(i, j int) bool {
					_, _, oi, _, _ := key(list[i])
					_, _, oj, _, _ := key(list[j])
					return oi < oj
				})
			}
			lanes = append(lanes, lane{source: source, items: list})
		}
		sort.Slice(lanes, func(i, j int) bool { return lanes[i].source < lanes[j].source })
		for round := 0; ; round++ {
			added := false
			for _, l := range lanes {
				if round < len(l.items) {
					out = append(out, l.items[round])
					added = true
				}
			}
			if !added {
				break
			}
		}
	}
	return out
}
