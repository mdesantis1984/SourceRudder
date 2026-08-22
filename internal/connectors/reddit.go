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

// RedditConfig is the seam Reddit itself requires from operators. This
// delivery is anonymous-only: there is no OAuth client ID, no client
// secret, and no bearer-token plumbing. The connector hits the public
// Reddit JSON endpoint with a deployment-specific User-Agent and
// surfaces a degraded SearchResponse when Reddit rejects the
// anonymous request.
//
// If a future delivery reintroduces authenticated Reddit access, the
// change MUST come with a fresh SDD cycle that updates the spec,
// design, and tests in this file. Anonymous-only is the current
// public contract.
type RedditConfig struct {
	BaseURL   string
	UserAgent string
}

type RedditConnector struct {
	cfg      RedditConfig
	cacheSvc *cache.Service
	client   *http.Client
}

// NewRedditConnector builds a Reddit connector from the anonymous-only
// configuration seam. Callers should always supply a non-empty
// UserAgent: Reddit's API rules require "a unique and descriptive
// User-Agent". The default below is deliberately generic; deployments
// that want production-grade compliance MUST override it via
// REDDIT_USER_AGENT or --reddit-user-agent.
func NewRedditConnector(cfg RedditConfig, cacheSvc *cache.Service) *RedditConnector {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://www.reddit.com"
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "ia-buscar/1.2 (anonymous-only)"
	}
	return &RedditConnector{
		cfg:      cfg,
		cacheSvc: cacheSvc,
		client:   &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *RedditConnector) Name() string { return "reddit" }

func (c *RedditConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	// Anonymous-only: cache key depends ONLY on (query, TimeRange).
	// It MUST NOT vary by User-Agent or by any per-deployment credential.
	cacheKey := cache.GenerateCacheKey(query, []string{"reddit"}, req.TimeRange)
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[reddit] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			if cachedResp.Results == nil {
				cachedResp.Results = []types.SearchResultItem{}
			}
			return cachedResp, nil
		}
	}

	results, err, degraded := c.doRedditRequest(ctx, query, maxResults)
	if err != nil {
		log.Printf("[reddit] search error: %v", err)
	}

	resp := &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{"reddit"},
		Strategy:    "reddit_anonymous",
		Cached:      false,
	}

	switch {
	case degraded != nil:
		resp.Strategy = "reddit_unconfigured"
		resp.Warnings = []string{"reddit_unconfigured: " + degraded.Error()}
		resp.Errors = []string{"reddit_unconfigured"}
	case err != nil:
		resp.Partial = true
		resp.Warnings = []string{err.Error()}
	}

	if resp.Results == nil {
		resp.Results = []types.SearchResultItem{}
	}

	c.cacheResults(ctx, cacheKey, resp)

	log.Printf("[reddit] search completed: query=%s, results=%d, degraded=%v, latency=%v", query, len(results), degraded != nil, time.Since(start))
	return resp, nil
}

// doRedditRequest runs the upstream anonymous call. It returns:
//
//	results: parsed SearchResultItem list (always non-nil; empty on error)
//	err:     non-nil when the request was attempted but failed
//	degraded: non-nil when the request was NOT attempted because
//	          Reddit's anonymous policy rejected it.
//
// The two error signals are mutually exclusive: a single call returns
// at most one of err or degraded.
func (c *RedditConnector) doRedditRequest(ctx context.Context, query string, maxResults int) ([]types.SearchResultItem, error, error) {
	apiURL := fmt.Sprintf("%s/search.json?q=%s&limit=%d",
		c.cfg.BaseURL, url.QueryEscape(query), maxResults)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return []types.SearchResultItem{}, fmt.Errorf("reddit transport build failed: %w", err), nil
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		observability.Default().RecordSearchDegraded("reddit", "transport")
		return []types.SearchResultItem{}, fmt.Errorf("reddit transport failure: %w", err), nil
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		// 401/403 from www.reddit.com on anonymous requests is the
		// expected policy outcome. Surface an explicit, actionable
		// degraded message instead of pretending the search happened.
		observability.Default().RecordSearchDegraded("reddit", "anonymous_blocked")
		return []types.SearchResultItem{}, nil, fmt.Errorf(
			"reddit rejected the anonymous request with status %d; "+
				"IA_Buscar ships anonymous-only — set REDDIT_USER_AGENT to a deployment-specific string (Reddit requires a unique User-Agent) and consider rotating the egress IP if the block is consistent",
			resp.StatusCode,
		)
	case http.StatusTooManyRequests:
		observability.Default().RecordSearchDegraded("reddit", "rate_limited")
		return []types.SearchResultItem{}, fmt.Errorf("reddit API error: 429 (rate limited)"), nil
	}

	if resp.StatusCode >= 400 {
		if resp.StatusCode >= 500 {
			observability.Default().RecordSearchDegraded("reddit", "upstream_http_5xx")
		} else {
			observability.Default().RecordSearchDegraded("reddit", "upstream_http_4xx")
		}
		return []types.SearchResultItem{}, fmt.Errorf("reddit API error: %d", resp.StatusCode), nil
	}

	var data struct {
		Data struct {
			Children []struct {
				Data struct {
					Title       string  `json:"title"`
					URL         string  `json:"url"`
					Subreddit   string  `json:"subreddit"`
					Score       int     `json:"score"`
					NumComments int     `json:"num_comments"`
					Author      string  `json:"author"`
					CreatedUTC  float64 `json:"created_utc"`
					SelfText    string  `json:"selftext"`
					Permalink   string  `json:"permalink"`
				} `json:"data"`
			} `json:"children"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return []types.SearchResultItem{}, err, nil
	}

	results := make([]types.SearchResultItem, 0, len(data.Data.Children))
	for _, child := range data.Data.Children {
		item := child.Data

		snippet := item.SelfText
		if snippet == "" {
			snippet = fmt.Sprintf("Score: %d | Comments: %d", item.Score, item.NumComments)
		} else if len(snippet) > 300 {
			snippet = snippet[:300] + "..."
		}

		postURL := item.URL
		if !strings.HasPrefix(postURL, "http") {
			postURL = "https://reddit.com" + item.Permalink
		}

		publishedAt := time.Unix(int64(item.CreatedUTC), 0)

		results = append(results, types.SearchResultItem{
			Title:       item.Title,
			URL:         postURL,
			Snippet:     snippet,
			Source:      "reddit",
			Type:        "post",
			Score:       float64(item.Score),
			PublishedAt: &publishedAt,
			Author:      item.Author,
			Tags:        []string{item.Subreddit},
			CitationID:  "reddit:" + item.Permalink,
		})
	}

	return results, nil, nil
}

func (c *RedditConnector) cacheResults(ctx context.Context, cacheKey string, resp *types.SearchResponse) {
	if c.cacheSvc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	c.cacheSvc.Set(ctx, cacheKey, data, resp.SourcesUsed)
}
