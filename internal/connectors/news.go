package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type NewsConnector struct {
	searxngURL string
	cacheSvc   *cache.Service
	httpClient *http.Client

	// rssURL is the Google News RSS search endpoint that supplements thin or
	// partial SearXNG answers. Empty (the default) keeps the connector
	// SearXNG-only; SetGoogleNewsRSS enables it.
	rssURL    string
	rssClient *http.Client
}

// newsRSSTimeout bounds one Google News RSS request.
const newsRSSTimeout = 8 * time.Second

// SetGoogleNewsRSS makes the connector supplement SearXNG with Google News RSS
// search at base (use connectors.GoogleNewsRSSURL in production, a test server
// URL in tests). It must be called before the connector serves requests.
// Without it the connector is SearXNG-only.
func (c *NewsConnector) SetGoogleNewsRSS(base string) {
	c.rssURL = strings.TrimSpace(base)
	c.rssClient = &http.Client{Timeout: newsRSSTimeout}
}

// GoogleNewsRSSURL is the production Google News RSS search endpoint.
const GoogleNewsRSSURL = googleNewsRSSURL

func NewNewsConnector(searxngURL string, cacheSvc *cache.Service) *NewsConnector {
	return &NewsConnector{
		searxngURL: searxngURL,
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *NewsConnector) Name() string { return "news" }

func (c *NewsConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	sources := []string{"news", "searxng"}
	if c.rssURL != "" {
		sources = append(sources, googleNewsEngine)
	}
	cacheKey := cache.GenerateSearchCacheKey(query, sources, req, maxResults)
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[news] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	results, err := c.searchSearxng(ctx, query, maxResults, req)
	if err != nil {
		log.Printf("[news] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"news"},
		Cached:      false,
	}
	recordSearxngError("news", results, err, resp)
	// The supplement runs before the timeRange check so that results it adds
	// are not reported as an empty answer.
	rssFailed := c.supplementWithRSS(ctx, query, maxResults, req, resp)
	recordTimeRangeEmpty(req, resp, err)

	cacheSearchResponse(ctx, c.cacheSvc, cacheKey, resp, rssFailed)

	log.Printf("[news] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *NewsConnector) searchSearxng(ctx context.Context, query string, maxResults int, req *types.SearchRequest) ([]types.SearchResultItem, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "news")
	params.Set("engines", "")
	if req.Language != "" {
		params.Set("language", req.Language)
	}
	if req.TimeRange != "" {
		params.Set("time_range", req.TimeRange)
	}
	if req.SafeSearch {
		params.Set("safesearch", "1")
	}

	apiURL := c.searxngURL + "/search?" + params.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return []types.SearchResultItem{}, err
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("searxng request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return []types.SearchResultItem{}, fmt.Errorf("searxng error: %d", resp.StatusCode)
	}

	var searxngResp struct {
		Results []struct {
			Title         string      `json:"title"`
			URL           string      `json:"url"`
			Content       string      `json:"content"`
			Source        string      `json:"source"`
			Engine        string      `json:"engine"`
			ParsedURL     interface{} `json:"parsed_url"`
			PublishedDate string      `json:"publishedDate"`
		} `json:"results"`
		UnresponsiveEngines [][]interface{} `json:"unresponsive_engines"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searxngResp); err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("searxng decode error: %w", err)
	}

	results := make([]types.SearchResultItem, 0, len(searxngResp.Results))
	for i, item := range searxngResp.Results {
		parsedDomain := extractDomain(item.ParsedURL)

		results = append(results, types.SearchResultItem{
			Title:      item.Title,
			URL:        item.URL,
			Snippet:    item.Content,
			Source:     item.Engine,
			Engine:     item.Engine,
			Type:       "article",
			Score:      float64(maxResults - i),
			CitationID: fmt.Sprintf("news:%s:%d", parsedDomain, i),
		})
	}

	if len(searxngResp.UnresponsiveEngines) > 0 {
		return results, newSearxngUnresponsiveError(searxngResp.UnresponsiveEngines)
	}

	return results, nil
}

// supplementWithRSS appends Google News RSS results after the SearXNG ones when
// SearXNG returned fewer than maxResults or was partial. Items repeating a URL
// or a normalized title already in the list are skipped, and scores keep
// strictly decreasing. A failed RSS request keeps what SearXNG gave and adds a
// warning that names it; it never marks the response partial by itself. It
// reports whether the RSS request failed.
func (c *NewsConnector) supplementWithRSS(ctx context.Context, query string, maxResults int, req *types.SearchRequest, resp *types.SearchResponse) bool {
	if c.rssURL == "" || (len(resp.Results) >= maxResults && !resp.Partial) {
		return false
	}
	items, err := fetchFeed(ctx, c.rssClient, googleNewsSearchURL(c.rssURL, query, req.Language, req.TimeRange))
	if err != nil {
		log.Printf("[news] %s supplement error: %v", googleNewsEngine, err)
		resp.Warnings = append(resp.Warnings, googleNewsEngine+" supplement failed: "+err.Error())
		observability.Default().RecordSearchDegraded("news", "google_news_rss_failed")
		return true
	}
	dedupe := newFeedDedupe()
	for _, r := range resp.Results {
		dedupe.seed(r.URL, r.Title, "")
	}
	score := float64(maxResults + 1)
	if n := len(resp.Results); n > 0 {
		score = resp.Results[n-1].Score
	}
	added := 0
	for _, item := range items {
		if added >= maxResults {
			break
		}
		if !dedupe.add(item.Link, item.Title, item.SourceName) {
			continue
		}
		score--
		resp.Results = append(resp.Results, googleNewsResult(item, "article", "news", score, len(resp.Results)))
		added++
	}
	return false
}

// googleNewsResult maps a Google News RSS item. Source is the publisher from
// <source>; the link stays a news.google.com URL (it redirects to the article
// and cannot be resolved without JavaScript).
func googleNewsResult(item feedItem, resultType, citationPrefix string, score float64, index int) types.SearchResultItem {
	publisher := item.SourceName
	domain := "news.google.com"
	if u, err := url.Parse(item.SourceURL); err == nil && u.Hostname() != "" {
		domain = u.Hostname()
		if publisher == "" {
			publisher = domain
		}
	}
	if publisher == "" {
		publisher = googleNewsEngine
	}
	snippet := item.Summary
	if strings.HasPrefix(snippet, item.Title) {
		snippet = strings.TrimSpace(strings.TrimPrefix(snippet, item.Title))
	}
	if snippet == "" {
		snippet = publisher
	}
	out := types.SearchResultItem{
		Title:      item.Title,
		URL:        item.Link,
		Snippet:    snippet,
		Source:     publisher,
		Engine:     googleNewsEngine,
		Type:       resultType,
		Score:      score,
		Author:     item.Author,
		CitationID: fmt.Sprintf("%s:%s:%d", citationPrefix, domain, index),
	}
	if !item.Published.IsZero() {
		published := item.Published
		out.PublishedAt = &published
	}
	return out
}
