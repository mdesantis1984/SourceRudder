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

type ImagesConnector struct {
	searxngURL string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewImagesConnector(searxngURL string, cacheSvc *cache.Service) *ImagesConnector {
	return &ImagesConnector{
		searxngURL: searxngURL,
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *ImagesConnector) Name() string { return "images" }

func (c *ImagesConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"images", "searxng"}, req.TimeRange)
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[images] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	results, err := c.searchSearxng(ctx, query, maxResults)
	if err != nil {
		log.Printf("[images] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"images"},
		Cached:      false,
	}
	recordSearxngError("images", results, err, resp)

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[images] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *ImagesConnector) searchSearxng(ctx context.Context, query string, maxResults int) ([]types.SearchResultItem, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("format", "json")
	params.Set("categories", "images")
	params.Set("engines", "")

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
			Title        string      `json:"title"`
			URL          string      `json:"url"`
			Content      string      `json:"content"`
			Source       string      `json:"source"`
			Engine       string      `json:"engine"`
			ImgSrc       string      `json:"img_src"`
			ThumbnailSrc string      `json:"thumbnail_src"`
			Template     string      `json:"template"`
			ParsedURL    interface{} `json:"parsed_url"`
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

		imageURL := item.ImgSrc
		if imageURL == "" {
			imageURL = item.URL
		}

		engine := item.Engine
		if engine == "" {
			engine = "searxng"
		}

		results = append(results, types.SearchResultItem{
			Title:      item.Title,
			URL:        imageURL,
			Snippet:    fmt.Sprintf("Source: %s | Engine: %s", item.Source, engine),
			Source:     engine,
			Type:       "image",
			Score:      float64(maxResults - i),
			CitationID: fmt.Sprintf("images:%s:%d", parsedDomain, i),
		})
	}

	if len(searxngResp.UnresponsiveEngines) > 0 {
		return results, newSearxngUnresponsiveError(searxngResp.UnresponsiveEngines)
	}

	return results, nil
}

func (c *ImagesConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
