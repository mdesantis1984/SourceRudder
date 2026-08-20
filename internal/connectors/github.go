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

type GitHubConnector struct {
	baseURL    string
	token      string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewGitHubConnector(token string, cacheSvc *cache.Service) *GitHubConnector {
	return &GitHubConnector{
		baseURL: "https://api.github.com",
		token:   token,
		cacheSvc:  cacheSvc,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *GitHubConnector) Name() string { return "github" }

func (c *GitHubConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	return c.searchRepositories(ctx, req)
}

func (c *GitHubConnector) searchRepositories(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := sanitizeQuery(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	var results []types.SearchResultItem
	var err error
	defer func() {
		observability.EndSpan(span, len(results), err)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"github", "repo"})
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[github] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	apiURL := fmt.Sprintf("%s/search/repositories?q=%s&per_page=%d", c.baseURL, query, maxResults)
	if req.Language != "" {
		apiURL += "&language=" + url.QueryEscape(req.Language)
	}

	results, err = c.doGitHubRequest(ctx, apiURL, "repo")
	if err != nil {
		log.Printf("[github] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"github"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		resp.Warnings = []string{err.Error()}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[github] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *GitHubConnector) SearchPR(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := sanitizeQuery(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	cacheKey := cache.GenerateCacheKey(query, []string{"github", "pr"})
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[github] cache hit for PR query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	state := "open"
	if req.Filters != nil {
		if s, ok := req.Filters["state"]; ok {
			state = s
		}
	}

	apiURL := fmt.Sprintf("%s/search/issues?q=%s+is:pr&state=%s&per_page=%d",
		c.baseURL, query, state, maxResults)

	results, err := c.doGitHubRequest(ctx, apiURL, "pr")
	if err != nil {
		log.Printf("[github] PR search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"github"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		resp.Warnings = []string{err.Error()}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[github] PR search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *GitHubConnector) SearchIssue(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := sanitizeQuery(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	cacheKey := cache.GenerateCacheKey(query, []string{"github", "issue"})
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[github] cache hit for issue query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	state := "open"
	if req.Filters != nil {
		if s, ok := req.Filters["state"]; ok {
			state = s
		}
	}

	apiURL := fmt.Sprintf("%s/search/issues?q=%s+is:issue&state=%s&per_page=%d",
		c.baseURL, query, state, maxResults)

	results, err := c.doGitHubRequest(ctx, apiURL, "issue")
	if err != nil {
		log.Printf("[github] issue search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"github"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		resp.Warnings = []string{err.Error()}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[github] issue search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *GitHubConnector) doGitHubRequest(ctx context.Context, apiURL string, resultType string) ([]types.SearchResultItem, error) {
	time.Sleep(1 * time.Second)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return []types.SearchResultItem{}, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return []types.SearchResultItem{}, fmt.Errorf("rate limited")
	}
	if resp.StatusCode >= 400 {
		return []types.SearchResultItem{}, fmt.Errorf("API error: %d", resp.StatusCode)
	}

	var data struct {
		Items []struct {
			Name        string `json:"name"`
			FullName    string `json:"full_name"`
			HTMLURL     string `json:"html_url"`
			Description string `json:"description"`
			Score       float64 `json:"score"`
			Language    string `json:"language"`
			Stargazers  int `json:"stargazers_count"`
			UpdatedAt   string `json:"updated_at"`
			Owner       struct {
				Login string `json:"login"`
			} `json:"owner"`
			Title     string `json:"title"`
			State     string `json:"state"`
			CreatedAt string `json:"created_at"`
		} `json:"items"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return []types.SearchResultItem{}, err
	}

	results := make([]types.SearchResultItem, 0, len(data.Items))
	for _, item := range data.Items {
		var title, snippet string
		if resultType == "repo" {
			title = item.FullName
			snippet = item.Description
		} else {
			title = item.Title
			snippet = item.Title
		}

		var publishedAt *time.Time
		if t, err := time.Parse(time.RFC3339, item.UpdatedAt); err == nil {
			publishedAt = &t
		} else if t, err := time.Parse(time.RFC3339, item.CreatedAt); err == nil {
			publishedAt = &t
		}

		results = append(results, types.SearchResultItem{
			Title:       title,
			URL:         item.HTMLURL,
			Snippet:     snippet,
			Source:      "github",
			Type:        resultType,
			Score:       item.Score,
			PublishedAt: publishedAt,
			Author:      item.Owner.Login,
			Tags:        []string{},
			CitationID:  "github:" + item.FullName,
		})
	}

	return results, nil
}

func (c *GitHubConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}

func sanitizeQuery(q string) string {
	q = strings.TrimSpace(q)
	q = url.QueryEscape(q)
	return q
}

func getMaxResults(max int, def int) int {
	if max <= 0 {
		return def
	}
	if max > 100 {
		return 100
	}
	return max
}
