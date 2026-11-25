package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/youtube"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// YouTubeDirectSearcher searches YouTube without SearXNG (yt-dlp
// ytsearchN:). *youtube.Service implements it.
type YouTubeDirectSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]youtube.SearchEntry, error)
}

const (
	// ytDlpEngine names direct results in Engine and Source.
	ytDlpEngine = "yt-dlp"
	// maxDirectSearchResults is the most entries asked from ytsearchN.
	maxDirectSearchResults = youtube.MaxSearchLimit
	maxSnippetRunes        = 300
)

type YouTubeConnector struct {
	searxngURL string
	cacheSvc   *cache.Service
	httpClient *http.Client
	direct     YouTubeDirectSearcher
}

// SetDirectSearch makes the connector search YouTube with yt-dlp first and
// keep SearXNG (youtube,brave) as the fallback. It must be called before the
// connector serves requests. Without it the connector is SearXNG-only.
func (c *YouTubeConnector) SetDirectSearch(s YouTubeDirectSearcher) { c.direct = s }

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

	cacheKey := c.cacheKey(query, maxResults, req)
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[youtube] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			return cachedResp, nil
		}
	}

	// yt-dlp ytsearch has no date filter, so a timeRange search goes straight
	// to SearXNG instead of silently ignoring the filter.
	fallbackWarning := ""
	if c.direct != nil && req.TimeRange == "" {
		items, err := c.searchDirect(ctx, query, maxResults)
		if err == nil && len(items) > 0 {
			resp := &types.SearchResponse{
				Query:       req.Query,
				Results:     items,
				SourcesUsed: []string{"youtube"},
			}
			c.cacheResults(ctx, cacheKey, resp, false)
			log.Printf("[youtube] search completed (yt-dlp): query=%s, results=%d, latency=%v", query, len(items), time.Since(start))
			return resp, nil
		}
		if err != nil {
			fallbackWarning = "yt-dlp search failed, used SearXNG fallback: " + err.Error()
			log.Printf("[youtube] yt-dlp search error, falling back to SearXNG: %v", err)
		} else {
			fallbackWarning = "yt-dlp search returned no results, used SearXNG fallback"
		}
		observability.Default().RecordSearchDegraded(c.Name(), "ytdlp_fallback")
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
	if fallbackWarning != "" {
		resp.Warnings = append(resp.Warnings, fallbackWarning)
	}
	recordSearxngError("youtube", results, err, resp)
	recordTimeRangeEmpty(req, resp, err)

	c.cacheResults(ctx, cacheKey, resp, fallbackWarning != "")

	log.Printf("[youtube] search completed: query=%s, results=%d, latency=%v", query, len(results), time.Since(start))
	return resp, nil
}

// cacheKey keeps direct and SearXNG-only deployments apart: the same query is
// answered by different backends and must not share cached results.
func (c *YouTubeConnector) cacheKey(query string, maxResults int, req *types.SearchRequest) string {
	sources := []string{"youtube", "searxng"}
	if c.direct != nil {
		sources = []string{"youtube", ytDlpEngine, "searxng"}
	}
	return cache.GenerateSearchCacheKey(query, sources, req, maxResults)
}

// searchDirect maps yt-dlp entries to result items, keeping yt-dlp's order.
func (c *YouTubeConnector) searchDirect(ctx context.Context, query string, maxResults int) ([]types.SearchResultItem, error) {
	limit := maxResults
	if limit > maxDirectSearchResults {
		limit = maxDirectSearchResults
	}
	entries, err := c.direct.Search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	items := make([]types.SearchResultItem, 0, len(entries))
	for _, e := range entries {
		if len(items) >= limit {
			break
		}
		item := types.SearchResultItem{
			Title:      e.Title,
			URL:        e.URL,
			Snippet:    directSnippet(e),
			Source:     ytDlpEngine,
			Engine:     ytDlpEngine,
			Type:       "video",
			Score:      float64(maxResults - len(items)),
			Author:     e.Channel,
			Tags:       []string{},
			CitationID: "youtube:" + e.ID,
		}
		if !e.PublishedAt.IsZero() {
			published := e.PublishedAt
			item.PublishedAt = &published
		}
		items = append(items, item)
	}
	return items, nil
}

// directSnippet joins channel, duration, view count and description.
func directSnippet(e youtube.SearchEntry) string {
	var parts []string
	if e.Channel != "" {
		parts = append(parts, e.Channel)
	}
	if d := youtube.FormatDuration(e.DurationSeconds); d != "" {
		parts = append(parts, d)
	}
	if e.Views > 0 {
		parts = append(parts, groupThousands(e.Views)+" views")
	}
	if e.Description != "" {
		parts = append(parts, e.Description)
	}
	snippet := strings.Join(parts, " · ")
	if r := []rune(snippet); len(r) > maxSnippetRunes {
		return string(r[:maxSnippetRunes]) + "..."
	}
	return snippet
}

func groupThousands(n int64) string {
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
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
	for _, item := range searxngResp.Results {
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
			Engine:     item.Engine,
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

// cacheResults stores resp; a fallback response is kept only briefly so the
// next search tries yt-dlp again soon.
func (c *YouTubeConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse, fallback bool) {
	cacheSearchResponse(ctx, c.cacheSvc, cacheKey, resp, fallback)
}
