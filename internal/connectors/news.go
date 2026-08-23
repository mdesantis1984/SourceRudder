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

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

type NewsConnector struct {
	searxngURL string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

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

	cacheKey := cache.GenerateCacheKey(query, []string{"news", "searxng"}, req.TimeRange)
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
	if len(results) == 0 && err != nil {
		resp.Partial = true
		resp.Warnings = []string{err.Error()}
	}

	c.cacheResults(ctx, cacheKey, resp)

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
			Title       string      `json:"title"`
			URL         string      `json:"url"`
			Content     string      `json:"content"`
			Source      string      `json:"source"`
			Engine      string      `json:"engine"`
			ParsedURL   interface{} `json:"parsed_url"`
			PublishedDate string    `json:"publishedDate"`
		} `json:"results"`
		UnresponsiveEngines [][]interface{} `json:"unresponsive_engines"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searxngResp); err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("searxng decode error: %w", err)
	}

	results := make([]types.SearchResultItem, 0, len(searxngResp.Results))
	for i, item := range searxngResp.Results {
		if i >= maxResults {
			break
		}
		parsedDomain := extractDomain(item.ParsedURL)

		results = append(results, types.SearchResultItem{
			Title:       item.Title,
			URL:         item.URL,
			Snippet:     item.Content,
			Source:      item.Engine,
			Type:        "article",
			Score:       float64(maxResults - i),
			CitationID:  fmt.Sprintf("news:%s:%d", parsedDomain, i),
		})
	}

	if len(results) == 0 && len(searxngResp.UnresponsiveEngines) > 0 {
		engines := make([]string, 0, len(searxngResp.UnresponsiveEngines))
		for _, entry := range searxngResp.UnresponsiveEngines {
			if len(entry) > 0 {
				if name, ok := entry[0].(string); ok {
					engines = append(engines, name)
				}
			}
		}
		observability.Default().RecordSearchDegraded("news", "unresponsive_engines")
		return []types.SearchResultItem{}, fmt.Errorf("searxng unresponsive engines: %v", engines)
	}

	return results, nil
}

func (c *NewsConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
