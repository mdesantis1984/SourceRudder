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

type NPMConnector struct {
	baseURL    string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewNPMConnector(cacheSvc *cache.Service) *NPMConnector {
	return &NPMConnector{
		baseURL:    "https://registry.npmjs.org",
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *NPMConnector) Name() string { return "npm" }

func (c *NPMConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"npm"})
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[npm] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	apiURL := fmt.Sprintf("%s/-/v1/search?text=%s&size=%d", c.baseURL, url.QueryEscape(query), maxResults)

	results, err := c.doNPMRequest(ctx, apiURL)
	if err != nil {
		log.Printf("[npm] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"npm"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		resp.Warnings = []string{err.Error()}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[npm] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *NPMConnector) doNPMRequest(ctx context.Context, apiURL string) ([]types.SearchResultItem, error) {
	time.Sleep(1 * time.Second)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return []types.SearchResultItem{}, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return []types.SearchResultItem{}, fmt.Errorf("API error: %d", resp.StatusCode)
	}

	var data struct {
		Objects []struct {
			Package struct {
				Name        string `json:"name"`
				Version     string `json:"version"`
				Description string `json:"description"`
				URL         string `json:"url"`
				Links       struct {
					Homepage string `json:"homepage"`
					Registry string `json:"registry"`
				} `json:"links"`
				Score float64 `json:"score"`
			} `json:"package"`
		} `json:"objects"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return []types.SearchResultItem{}, err
	}

	results := make([]types.SearchResultItem, 0, len(data.Objects))
	for _, item := range data.Objects {
		pkgURL := item.Package.Links.Homepage
		if pkgURL == "" {
			pkgURL = fmt.Sprintf("https://www.npmjs.com/package/%s", item.Package.Name)
		}

		results = append(results, types.SearchResultItem{
			Title:       item.Package.Name,
			URL:         pkgURL,
			Snippet:     item.Package.Description,
			Source:      "npm",
			Type:        "package",
			Score:       item.Package.Score,
			Author:      item.Package.Name,
			CitationID:  fmt.Sprintf("npm:%s", item.Package.Name),
		})
	}

	return results, nil
}

func (c *NPMConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
