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

type AcademicConnector struct {
	searxngURL string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewAcademicConnector(searxngURL string, cacheSvc *cache.Service) *AcademicConnector {
	return &AcademicConnector{
		searxngURL: searxngURL,
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *AcademicConnector) Name() string { return "academic" }

func (c *AcademicConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"academic", "searxng"})
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[academic] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	results, err := c.searchSearxng(ctx, query, maxResults, req)
	if err != nil {
		log.Printf("[academic] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"academic"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		resp.Partial = true
		resp.Warnings = []string{err.Error()}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[academic] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *AcademicConnector) searchSearxng(ctx context.Context, query string, maxResults int, req *types.SearchRequest) ([]types.SearchResultItem, error) {
	time.Sleep(500 * time.Millisecond)

	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "science")
	params.Set("engines", "arxiv,duckduckgo")
	if req.Language != "" {
		params.Set("language", req.Language)
	} else {
		params.Set("language", "en")
	}
	if req.TimeRange != "" {
		params.Set("time_range", req.TimeRange)
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
		if !isAcademicURL(item.URL) {
			continue
		}

		engine := item.Engine
		if engine == "" {
			engine = "searxng"
		}

		publishedAt := parsePublishedDate(item.PublishedDate)

		results = append(results, types.SearchResultItem{
			Title:       item.Title,
			URL:         item.URL,
			Snippet:     truncateSnippet(item.Content),
			Source:      engine,
			Type:        "paper",
			Score:       float64(maxResults - i),
			PublishedAt: publishedAt,
			Tags:        []string{},
			CitationID:  fmt.Sprintf("academic:%s:%d", extractDomain(item.ParsedURL), i),
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
		observability.Default().RecordSearchDegraded("academic", "unresponsive_engines")
		return []types.SearchResultItem{}, fmt.Errorf("searxng: %d unresponsive engines %v: timeout", len(engines), engines)
	}

	return results, nil
}

func isAcademicURL(urlStr string) bool {
	academicHosts := []string{
		"arxiv.org",
		"scholar.google",
		"semanticscholar.org",
		"researchgate.net",
		"academia.edu",
		"pubmed.ncbi.nlm.nih.gov",
		"ieee.org",
		"acm.org",
		"springer.com",
		"nature.com",
		"sciencedirect.com",
		"wiley.com",
		"arxiv.org/abs",
		"doi.org",
	}
	urlLower := strings.ToLower(urlStr)
	for _, host := range academicHosts {
		if strings.Contains(urlLower, host) {
			return true
		}
	}
	return strings.Contains(urlLower, "pdf") || strings.Contains(urlLower, "paper") || strings.Contains(urlLower, "research")
}

func parsePublishedDate(dateStr string) *time.Time {
	if dateStr == "" {
		return nil
	}
	formats := []string{"2006-01-02", "2006-01-02T15:04:05Z", "2006-01-02T15:04:05"}
	for _, format := range formats {
		if t, err := time.Parse(format, dateStr); err == nil {
			return &t
		}
	}
	return nil
}

func (c *AcademicConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
