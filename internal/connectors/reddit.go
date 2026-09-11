package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// RedditConfig selects the SearXNG endpoint that indexes public Reddit posts.
// BaseURL is retained only as a compatibility seam for in-package tests and is
// treated as a SearXNG URL; it is never used to call Reddit's API.
type RedditConfig struct {
	SearxngURL string
	BaseURL    string
	UserAgent  string
}

type RedditConnector struct {
	cfg      RedditConfig
	cacheSvc *cache.Service
	client   *http.Client
}

// NewRedditConnector searches public Reddit posts indexed by the locally
// configured SearXNG instance. It does not call Reddit's API.
func NewRedditConnector(cfg RedditConfig, cacheSvc *cache.Service) *RedditConnector {
	return &RedditConnector{
		cfg:      cfg,
		cacheSvc: cacheSvc,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *RedditConnector) Name() string { return "reddit" }

func (c *RedditConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, redditCacheDimensions(maxResults, req), req.TimeRange)
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[reddit] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			if cachedResp.Results == nil {
				cachedResp.Results = []types.SearchResultItem{}
			}
			return cachedResp, nil
		}
	}

	results, err := c.searchSearxng(ctx, query, maxResults, req)
	if err != nil {
		log.Printf("[reddit] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"reddit"},
		Strategy:    "searxng_reddit_index",
		Cached:      false,
	}
	recordSearxngError("reddit", results, err, resp)

	if resp.Results == nil {
		resp.Results = []types.SearchResultItem{}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[reddit] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *RedditConnector) searchSearxng(ctx context.Context, query string, maxResults int, searchReq *types.SearchRequest) ([]types.SearchResultItem, error) {
	params := url.Values{}
	params.Set("q", query+" site:reddit.com")
	params.Set("format", "json")
	params.Set("engines", "")
	if searchReq.Language != "" {
		params.Set("language", searchReq.Language)
	}
	if searchReq.TimeRange != "" {
		params.Set("time_range", searchReq.TimeRange)
	}
	if searchReq.SafeSearch {
		params.Set("safesearch", "1")
	}

	searxngURL := c.cfg.SearxngURL
	if searxngURL == "" {
		searxngURL = c.cfg.BaseURL
	}
	apiURL := searxngURL + "/search?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("searxng request build failed: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("searxng request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return []types.SearchResultItem{}, fmt.Errorf("searxng error: %d", resp.StatusCode)
	}

	var data struct {
		Results []struct {
			Title         string      `json:"title"`
			URL           string      `json:"url"`
			Content       string      `json:"content"`
			Engine        string      `json:"engine"`
			PublishedDate string      `json:"publishedDate"`
			ParsedURL     interface{} `json:"parsed_url"`
		} `json:"results"`
		UnresponsiveEngines [][]interface{} `json:"unresponsive_engines"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("searxng decode error: %w", err)
	}

	results := make([]types.SearchResultItem, 0, maxResults)
	seen := make(map[string]struct{})
	for _, item := range data.Results {
		if len(results) >= maxResults {
			break
		}
		canonicalURL, ok := canonicalRedditPostURL(item.URL)
		if !ok {
			continue
		}
		if _, ok := seen[canonicalURL]; ok {
			continue
		}
		seen[canonicalURL] = struct{}{}
		engine := item.Engine
		if engine == "" {
			engine = "searxng"
		}
		results = append(results, types.SearchResultItem{
			Title:        item.Title,
			URL:          canonicalURL,
			Snippet:      item.Content,
			Source:       engine,
			Type:         "post",
			Score:        float64(maxResults - len(results)),
			PublishedAt:  parsePublishedDate(item.PublishedDate),
			CitationID:   "reddit:" + canonicalURL,
			CanonicalURL: canonicalURL,
		})
	}

	if len(data.UnresponsiveEngines) > 0 {
		return results, newSearxngUnresponsiveError(data.UnresponsiveEngines)
	}
	return results, nil
}

func canonicalRedditPostURL(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "reddit.com", "www.reddit.com", "old.reddit.com", "np.reddit.com":
	default:
		return "", false
	}
	if strings.Contains(parsed.EscapedPath(), "%") {
		return "", false
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(segments) < 4 || !strings.EqualFold(segments[0], "r") || segments[1] == "" || !strings.EqualFold(segments[2], "comments") || !isBase36ID(segments[3]) {
		return "", false
	}
	return "https://www.reddit.com/r/" + segments[1] + "/comments/" + strings.ToLower(segments[3]) + "/", true
}

func isBase36ID(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			if char < 'a' || char > 'z' {
				if char < 'A' || char > 'Z' {
					return false
				}
			}
		}
	}
	return true
}

func redditCacheDimensions(maxResults int, req *types.SearchRequest) []string {
	dimensions := []string{"reddit_searxng", fmt.Sprintf("limit=%d", maxResults), "language=" + req.Language, fmt.Sprintf("safesearch=%t", req.SafeSearch)}
	keys := make([]string, 0, len(req.Filters))
	for key := range req.Filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		dimensions = append(dimensions, "filter="+key+"="+req.Filters[key])
	}
	return dimensions
}

func (c *RedditConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
