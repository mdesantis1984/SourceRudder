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

type StackOverflowConnector struct {
	baseURL    string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewStackOverflowConnector(cacheSvc *cache.Service) *StackOverflowConnector {
	return &StackOverflowConnector{
		baseURL:    "https://api.stackexchange.com/2.3",
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *StackOverflowConnector) Name() string { return "stackoverflow" }

func (c *StackOverflowConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	var err error

	cacheKey := cache.GenerateCacheKey(query, []string{"stackoverflow"}, "")
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[stackoverflow] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err = json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	values := url.Values{}
	values.Set("order", "desc")
	values.Set("sort", "relevance")
	values.Set("site", "stackoverflow")
	values.Set("pagesize", fmt.Sprintf("%d", maxResults))

	apiURL := fmt.Sprintf("%s/search/advanced?%s&q=%s", c.baseURL, values.Encode(), url.QueryEscape(query))

	results, err := c.doStackOverflowRequest(ctx, apiURL)
	if err != nil {
		log.Printf("[stackoverflow] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"stackoverflow"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		recordDegraded("stackoverflow", "transport", resp, err)
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[stackoverflow] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *StackOverflowConnector) doStackOverflowRequest(ctx context.Context, apiURL string) ([]types.SearchResultItem, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return []types.SearchResultItem{}, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return []types.SearchResultItem{}, fmt.Errorf("API error: %d", resp.StatusCode)
	}

	var data struct {
		Items []struct {
			QuestionID  int      `json:"question_id"`
			Title       string   `json:"title"`
			Link        string   `json:"link"`
			Score       int      `json:"score"`
			AnswerCount int      `json:"answer_count"`
			Tags        []string `json:"tags"`
			Owner       struct {
				DisplayName string `json:"display_name"`
			} `json:"owner"`
			CreationDate int64 `json:"creation_date"`
		} `json:"items"`
		QuotaMax     int `json:"quota_max"`
		QuotaRemaining int `json:"quota_remaining"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return []types.SearchResultItem{}, err
	}

	results := make([]types.SearchResultItem, 0, len(data.Items))
	for _, item := range data.Items {
		snippet := fmt.Sprintf("Score: %d, Answers: %d", item.Score, item.AnswerCount)

		publishedAt := time.Unix(item.CreationDate, 0)

		results = append(results, types.SearchResultItem{
			Title:       item.Title,
			URL:         item.Link,
			Snippet:     snippet,
			Source:      "stackoverflow",
			Type:        "question",
			Score:       float64(item.Score),
			PublishedAt: &publishedAt,
			Author:      item.Owner.DisplayName,
			Tags:        item.Tags,
			CitationID:  fmt.Sprintf("stackoverflow:%d", item.QuestionID),
		})
	}

	return results, nil
}

func (c *StackOverflowConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
