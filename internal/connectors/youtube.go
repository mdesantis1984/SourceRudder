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

type YouTubeConnector struct {
	searxngURL string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewYouTubeConnector(searxngURL string, cacheSvc *cache.Service) *YouTubeConnector {
	return &YouTubeConnector{
		searxngURL: searxngURL,
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *YouTubeConnector) Name() string { return "youtube" }

func (c *YouTubeConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"youtube", "searxng"}, req.TimeRange)
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[youtube] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	results, err := c.searchSearxng(ctx, query, maxResults, req)
	if err != nil {
		log.Printf("[youtube] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"youtube"},
		Cached:      false,
	}
	recordSearxngError("youtube", results, err, resp)

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[youtube] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *YouTubeConnector) searchSearxng(ctx context.Context, query string, maxResults int, req *types.SearchRequest) ([]types.SearchResultItem, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("engines", "youtube,brave")
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
			Title         string      `json:"title"`
			URL           string      `json:"url"`
			Content       string      `json:"content"`
			Source        string      `json:"source"`
			Engine        string      `json:"engine"`
			Template      string      `json:"template"`
			ParsedURL     interface{} `json:"parsed_url"`
			Thumbnail     string      `json:"thumbnail"`
			PublishedDate string      `json:"publishedDate"`
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

		if !isVideoURL(item.URL) {
			continue
		}

		engine := item.Engine
		if engine == "" {
			engine = "searxng"
		}

		videoID := extractYouTubeVideoID(item.URL)

		results = append(results, types.SearchResultItem{
			Title:      item.Title,
			URL:        item.URL,
			Snippet:    truncateSnippet(item.Content),
			Source:     engine,
			Type:       "video",
			Score:      float64(maxResults - len(results)),
			Tags:       []string{},
			CitationID: "youtube:" + videoID,
		})
	}

	if len(searxngResp.UnresponsiveEngines) > 0 {
		return results, newSearxngUnresponsiveError(searxngResp.UnresponsiveEngines)
	}

	return results, nil
}

func isVideoURL(urlStr string) bool {
	videoHosts := []string{
		"youtube.com",
		"youtu.be",
		"youtube-nocookie.com",
		"bilibili.com",
		"vimeo.com",
		"dailymotion.com",
		"twitch.tv",
	}
	urlLower := strings.ToLower(urlStr)
	for _, host := range videoHosts {
		if strings.Contains(urlLower, host) {
			return true
		}
	}
	return false
}

func extractYouTubeVideoID(urlStr string) string {
	if strings.Contains(urlStr, "youtu.be/") {
		parts := strings.Split(urlStr, "youtu.be/")
		if len(parts) > 1 {
			id := parts[1]
			if idx := strings.Index(id, "?"); idx > 0 {
				return id[:idx]
			}
			if idx := strings.Index(id, "&"); idx > 0 {
				return id[:idx]
			}
			return id
		}
	}
	if strings.Contains(urlStr, "watch?v=") {
		parts := strings.Split(urlStr, "watch?v=")
		if len(parts) > 1 {
			id := parts[1]
			if idx := strings.Index(id, "&"); idx > 0 {
				return id[:idx]
			}
			return id
		}
	}
	return "unknown"
}

func truncateSnippet(snippet string) string {
	if len(snippet) > 300 {
		return snippet[:300] + "..."
	}
	return snippet
}

func (c *YouTubeConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
