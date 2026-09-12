package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type PyPIConnector struct {
	baseURL    string
	cacheSvc   *cache.Service
	httpClient *http.Client
}

func NewPyPIConnector(cacheSvc *cache.Service) *PyPIConnector {
	return &PyPIConnector{
		baseURL:    "https://pypi.org",
		cacheSvc:   cacheSvc,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *PyPIConnector) Name() string { return "pypi" }

func (c *PyPIConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"pypi"}, "")
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[pypi] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	results, err := c.searchPyPI(ctx, query, maxResults)
	if err != nil {
		log.Printf("[pypi] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"pypi"},
		Cached:      false,
	}
	if len(results) == 0 && err != nil {
		resp.Warnings = []string{err.Error()}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[pypi] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

func (c *PyPIConnector) searchPyPI(ctx context.Context, query string, maxResults int) ([]types.SearchResultItem, error) {
	exactMatch, err := c.tryExactMatch(ctx, query)
	if err == nil && exactMatch != nil {
		log.Printf("[pypi] exact match found for: %s", query)
		return []types.SearchResultItem{*exactMatch}, nil
	}

	prefixResults, err := c.searchByPrefix(ctx, query, maxResults)
	if err == nil && len(prefixResults) > 0 {
		return prefixResults, nil
	}

	log.Printf("[pypi] no results found for query: %s", query)
	return []types.SearchResultItem{}, nil
}

func (c *PyPIConnector) tryExactMatch(ctx context.Context, pkgName string) (*types.SearchResultItem, error) {
	apiURL := fmt.Sprintf("%s/pypi/%s/json", c.baseURL, url.PathEscape(strings.ToLower(pkgName)))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		return nil, fmt.Errorf("package not found")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API error: %d", resp.StatusCode)
	}

	var data struct {
		Info struct {
			Name        string `json:"name"`
			Summary     string `json:"summary"`
			Version     string `json:"version"`
			HomePage    string `json:"home_page"`
			ProjectURL  string `json:"project_url"`
			PackageURL  string `json:"package_url"`
			ProjectUrls map[string]string `json:"project_urls"`
		} `json:"info"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	homeURL := data.Info.HomePage
	if homeURL == "" {
		homeURL = data.Info.ProjectURL
	}
	if homeURL == "" {
		homeURL = data.Info.PackageURL
	}

	return &types.SearchResultItem{
		Title:      fmt.Sprintf("%s (%s)", data.Info.Name, data.Info.Version),
		URL:        homeURL,
		Snippet:    data.Info.Summary,
		Source:     "pypi",
		Type:       "package",
		Score:      1.0,
		CitationID: fmt.Sprintf("pypi:%s", data.Info.Name),
	}, nil
}

func (c *PyPIConnector) searchByPrefix(ctx context.Context, query string, maxResults int) ([]types.SearchResultItem, error) {
	prefix := strings.ToLower(query)
	apiURL := fmt.Sprintf("%s/simple/%s/", c.baseURL, url.PathEscape(prefix))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", "SourceRudder/3.0.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("prefix search failed: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return c.parseSimpleIndex(string(body), query, maxResults)
}

func (c *PyPIConnector) parseSimpleIndex(html string, query string, maxResults int) ([]types.SearchResultItem, error) {
	results := make([]types.SearchResultItem, 0)

	re := regexp.MustCompile(`<a[^>]+href="https://pypi\.org/project/([^"]+)"[^>]*>([^<]+)</a>`)
	matches := re.FindAllStringSubmatch(html, -1)

	queryLower := strings.ToLower(query)
	seen := make(map[string]bool)

	for _, match := range matches {
		if len(results) >= maxResults {
			break
		}

		pkgName := match[1]
		if seen[pkgName] {
			continue
		}
		seen[pkgName] = true

		if !strings.HasPrefix(strings.ToLower(pkgName), queryLower) {
			continue
		}

		results = append(results, types.SearchResultItem{
			Title:      pkgName,
			URL:        fmt.Sprintf("https://pypi.org/project/%s", pkgName),
			Snippet:    "",
			Source:     "pypi",
			Type:       "package",
			Score:      0.8,
			CitationID: fmt.Sprintf("pypi:%s", pkgName),
		})
	}

	return results, nil
}

func (c *PyPIConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
