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

type NuGetConnector struct {
	baseURL    string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewNuGetConnector(cacheSvc *cache.Service) *NuGetConnector {
	return &NuGetConnector{
		baseURL:    "https://api.nuget.org/v3-flatcontainer",
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *NuGetConnector) Name() string { return "nuget" }

func (c *NuGetConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"nuget"}, "")
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[nuget] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	apiURL := fmt.Sprintf("https://azuresearch-usnc.nuget.org/query?searchTerm=%s&take=%d&semVerLevel=2.0.0", url.QueryEscape(query), maxResults)

	results, err := c.doNuGetRequest(ctx, apiURL)
	if err != nil {
		log.Printf("[nuget] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"nuget"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		recordDegraded("nuget", "transport", resp, err)
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[nuget] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *NuGetConnector) doNuGetRequest(ctx context.Context, apiURL string) ([]types.SearchResultItem, error) {
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
		TotalHits int `json:"totalHits"`
		Data      []struct {
			ID            string `json:"id"`
			Version       string `json:"version"`
			Description   string `json:"description"`
			Title         string `json:"title"`
			ProjectURL    string `json:"projectUrl"`
			IconURL       string `json:"iconUrl"`
			Authors       []string `json:"authors"`
			Owners        []string `json:"owners"`
			Tags          []string `json:"tags"`
			TotalDownloads int64  `json:"totalDownloads"`
			Verified      bool    `json:"verified"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return []types.SearchResultItem{}, err
	}

	results := make([]types.SearchResultItem, 0, len(data.Data))
	for _, item := range data.Data {
		snippet := item.Description
		if snippet == "" {
			snippet = fmt.Sprintf("Downloads: %d", item.TotalDownloads)
		}

		title := item.Title
		if title == "" {
			title = item.ID
		}

		results = append(results, types.SearchResultItem{
			Title:       title,
			URL:         item.ProjectURL,
			Snippet:     snippet,
			Source:      "nuget",
			Type:        "package",
			Score:       float64(item.TotalDownloads),
			Author:      strings.Join(item.Authors, ", "),
			Tags:        item.Tags,
			CitationID:  fmt.Sprintf("nuget:%s", item.ID),
		})
	}

	return results, nil
}

func (c *NuGetConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
