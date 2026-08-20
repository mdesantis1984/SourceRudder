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

type DockerHubConnector struct {
	baseURL    string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewDockerHubConnector(cacheSvc *cache.Service) *DockerHubConnector {
	return &DockerHubConnector{
		baseURL:    "https://hub.docker.com",
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *DockerHubConnector) Name() string { return "dockerhub" }

func (c *DockerHubConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"dockerhub"})
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[dockerhub] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	apiURL := fmt.Sprintf("%s/v2/search/repositories?query=%s&page_size=%d", c.baseURL, url.QueryEscape(query), maxResults)

	results, err := c.doDockerHubRequest(ctx, apiURL)
	if err != nil {
		log.Printf("[dockerhub] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"dockerhub"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		resp.Warnings = []string{err.Error()}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[dockerhub] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *DockerHubConnector) doDockerHubRequest(ctx context.Context, apiURL string) ([]types.SearchResultItem, error) {
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
		Count   int `json:"count"`
		Results []struct {
			RepoName        string `json:"repo_name"`
			Namespace       string `json:"namespace"`
			ShortDescription string `json:"short_description"`
			StarCount       int    `json:"star_count"`
			PullCount       int64  `json:"pull_count"`
			IsOfficial      bool   `json:"is_official"`
			IsAutomated     bool   `json:"is_automated"`
		} `json:"results"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return []types.SearchResultItem{}, err
	}

	results := make([]types.SearchResultItem, 0, len(data.Results))
	for _, item := range data.Results {
		fullName := item.Namespace + "/" + item.RepoName
		urlPath := "r"
		if item.IsOfficial {
			fullName = item.RepoName
			urlPath = "_"
		}

		results = append(results, types.SearchResultItem{
			Title:       fullName,
			URL:         fmt.Sprintf("https://hub.docker.com/%s/%s", urlPath, fullName),
			Snippet:     item.ShortDescription,
			Source:      "dockerhub",
			Type:        "repository",
			Score:       float64(item.StarCount),
			Author:      item.Namespace,
			CitationID:  fmt.Sprintf("dockerhub:%s", fullName),
		})
	}

	return results, nil
}

func (c *DockerHubConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}