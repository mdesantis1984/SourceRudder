package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type WebConnector struct {
	searxngURL string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewWebConnector(searxngURL string, cacheSvc *cache.Service) *WebConnector {
	return &WebConnector{
		searxngURL: searxngURL,
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *WebConnector) Name() string { return "web" }

func (c *WebConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	// maxResults is how many unique results the caller needs to see. It
	// drives the page-2 fill rule and the cache key; the connector itself
	// keeps every unique result it fetched.
	maxResults := getMaxResults(req.MaxResults, defaultWebMaxResults)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateSearchCacheKey(query, []string{"searxng"}, req, maxResults)
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[web] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	var results []types.SearchResultItem
	var err error
	pages := 0

	if c.searxngURL != "" {
		results, pages, err = c.searchSearxng(ctx, query, maxResults, req)
	} else {
		results, err = c.searchGeneric(ctx, query, maxResults, req)
	}

	if err != nil {
		log.Printf("[web] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		Strategy:    "searxng",
		SourcesUsed: []string{"searxng"},
		Cached:      false,
		// SearxngPages tells the MCP layer where lazy paging resumes.
		SearxngPages: pages,
	}
	recordWebSearchError(results, err, resp)

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[web] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

// defaultWebMaxResults is the search_web default when maxResults is omitted
// or non-positive. SearXNG returns about 30 results per page, so the previous
// default of 10 threw most of them away.
const defaultWebMaxResults = 20

// searxngPage is one decoded SearXNG response page.
type searxngPage struct {
	items   []searxngItem
	engines [][]interface{}
}

type searxngItem struct {
	Title     string      `json:"title"`
	URL       string      `json:"url"`
	Content   string      `json:"content"`
	Source    string      `json:"source"`
	Engine    string      `json:"engine"`
	ParsedURL interface{} `json:"parsed_url"`
}

// MaxSearxngPages bounds the SearXNG pages fetched for one stored result set,
// counting the pages fetched at search time and the lazy ones.
const MaxSearxngPages = 5

// searchSearxng returns the unique results, how many SearXNG pages were read
// successfully, and any degradation error.
func (c *WebConnector) searchSearxng(ctx context.Context, query string, maxResults int, req *types.SearchRequest) ([]types.SearchResultItem, int, error) {
	page1, err := c.fetchSearxngPage(ctx, query, 1, req)
	if err != nil {
		return []types.SearchResultItem{}, 0, err
	}
	pages := 1

	items := make([]searxngItem, 0, maxResults)
	seen := make(map[string]struct{}, maxResults)
	appendUnique := func(page *searxngPage) {
		for _, item := range page.items {
			if _, dup := seen[item.URL]; dup {
				continue
			}
			seen[item.URL] = struct{}{}
			items = append(items, item)
		}
	}
	appendUnique(page1)
	engines := append([][]interface{}(nil), page1.engines...)

	var page2Err error
	if len(items) < maxResults {
		page2, err := c.fetchSearxngPage(ctx, query, 2, req)
		if err != nil {
			page2Err = fmt.Errorf("searxng page 2 failed: %w", err)
		} else {
			appendUnique(page2)
			engines = append(engines, page2.engines...)
			pages = 2
		}
	}

	results := make([]types.SearchResultItem, 0, len(items))
	for i, item := range items {
		results = append(results, types.SearchResultItem{
			Title:      item.Title,
			URL:        item.URL,
			Snippet:    item.Content,
			Source:     "searxng",
			Engine:     item.Engine,
			Type:       "web",
			Score:      float64(maxResults - i),
			CitationID: fmt.Sprintf("web:%s", extractDomain(item.ParsedURL)),
		})
	}

	engines = dedupeUnresponsiveEngines(engines)
	if len(engines) > 0 {
		unresponsive := newSearxngUnresponsiveError(engines)
		if page2Err != nil {
			return results, pages, &webPageTwoError{unresponsive: unresponsive, page2: page2Err}
		}
		return results, pages, unresponsive
	}
	if page2Err != nil {
		return results, pages, &webPageTwoError{page2: page2Err}
	}
	return results, pages, nil
}

// NextPage requests one more SearXNG page for an existing result set and
// returns only the results whose URL is not in seen, in page order, scored
// strictly below lastScore. It makes exactly one upstream request and is used
// only when a caller pages past the stored set; req supplies the original
// language, time range and safe-search values.
func (c *WebConnector) NextPage(ctx context.Context, query string, pageno int, req *types.SearchRequest, seen map[string]struct{}, lastScore float64) ([]types.SearchResultItem, error) {
	if c.searxngURL == "" || pageno < 2 || pageno > MaxSearxngPages {
		return nil, nil
	}
	page, err := c.fetchSearxngPage(ctx, query, pageno, req)
	if err != nil {
		return nil, err
	}
	dup := make(map[string]struct{}, len(page.items))
	out := make([]types.SearchResultItem, 0, len(page.items))
	for _, item := range page.items {
		if _, ok := seen[item.URL]; ok {
			continue
		}
		if _, ok := dup[item.URL]; ok {
			continue
		}
		dup[item.URL] = struct{}{}
		out = append(out, types.SearchResultItem{
			Title:      item.Title,
			URL:        item.URL,
			Snippet:    item.Content,
			Source:     "searxng",
			Engine:     item.Engine,
			Type:       "web",
			Score:      lastScore - float64(len(out)+1),
			CitationID: fmt.Sprintf("web:%s", extractDomain(item.ParsedURL)),
		})
	}
	return out, nil
}

// webPageTwoError carries a failed page-2 request (and any unresponsive
// engines page 1 reported) back to Search without failing the call.
type webPageTwoError struct {
	unresponsive error
	page2        error
}

func (e *webPageTwoError) Error() string { return e.page2.Error() }

func dedupeUnresponsiveEngines(entries [][]interface{}) [][]interface{} {
	seen := make(map[string]struct{}, len(entries))
	out := make([][]interface{}, 0, len(entries))
	for _, entry := range entries {
		key := fmt.Sprint(entry...)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, entry)
	}
	return out
}

func (c *WebConnector) fetchSearxngPage(ctx context.Context, query string, pageno int, req *types.SearchRequest) (*searxngPage, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("engines", "")
	if pageno > 1 {
		params.Set("pageno", strconv.Itoa(pageno))
	}
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
		return nil, err
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("searxng request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("searxng error: %d", resp.StatusCode)
	}

	var searxngResp struct {
		Results             []searxngItem   `json:"results"`
		UnresponsiveEngines [][]interface{} `json:"unresponsive_engines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searxngResp); err != nil {
		return nil, fmt.Errorf("searxng decode error: %w", err)
	}
	return &searxngPage{items: searxngResp.Results, engines: searxngResp.UnresponsiveEngines}, nil
}

func (c *WebConnector) searchGeneric(ctx context.Context, query string, maxResults int, req *types.SearchRequest) ([]types.SearchResultItem, error) {
	return []types.SearchResultItem{
		{
			Title:      "SearxNG not configured",
			URL:        "",
			Snippet:    "Web search requires SearxNG to be configured. Set --searxng-url flag.",
			Source:     "searxng",
			Type:       "web",
			Score:      0,
			CitationID: "searxng:unconfigured",
		},
	}, nil
}

func (c *WebConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	cacheSearxngResponse(ctx, c.cacheSvc, cacheKey, resp)
}

func extractDomain(parsedURL interface{}) string {
	if parsedURL == nil {
		return "unknown"
	}
	switch v := parsedURL.(type) {
	case map[string]interface{}:
		if domain, ok := v["domain"].(string); ok {
			return domain
		}
	case string:
		return v
	}
	return "unknown"
}

// recordWebSearchError extends recordSearxngError with the page-2 failure
// path: page-1 results are kept and the failure is surfaced as a degradation
// warning instead of failing the call.
func recordWebSearchError(results []types.SearchResultItem, err error, resp *types.SearchResponse) {
	p2, ok := err.(*webPageTwoError)
	if !ok {
		recordSearxngError("web", results, err, resp)
		return
	}
	if p2.unresponsive != nil {
		recordSearxngError("web", results, p2.unresponsive, resp)
	}
	recordDegraded("web", "page2_failed", resp, p2.page2)
}
