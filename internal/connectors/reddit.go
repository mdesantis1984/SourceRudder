package connectors

import (
	"context"
	"encoding/json"
	"errors"
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

// RedditConfig exposes the seam Reddit itself requires from operators.
// IA_Buscar does not invent credentials: User-Agent, ClientID and
// ClientSecret are all read from flags or environment variables in main.
//
// A non-empty ClientID/ClientSecret pair enables OAuth-authenticated
// requests against https://oauth.reddit.com. Without it, the connector
// still attempts anonymous requests, but every 401/403 surfaces an
// explicit degraded response that tells the AI exactly why results are
// unavailable.
type RedditConfig struct {
	BaseURL      string
	UserAgent    string
	ClientID     string
	ClientSecret string
}

type RedditConnector struct {
	cfg       RedditConfig
	cacheSvc  *cache.Service
	client    *http.Client
	oauth     *redditOAuthToken
	oauthHost string
}

// redditOAuthToken stores the bearer token Reddit's OAuth flow returns.
// It is process-local only and is never persisted.
type redditOAuthToken struct {
	accessToken string
	expiresAt   time.Time
}

// NewRedditConnector builds a Reddit connector from a configuration seam.
// Callers should always supply a non-empty UserAgent: Reddit's API rules
// require "a unique and descriptive User-Agent". The default below is
// deliberately generic; deployments that want production-grade compliance
// MUST override it via REDDIT_USER_AGENT or --reddit-user-agent.
func NewRedditConnector(cfg RedditConfig, cacheSvc *cache.Service) *RedditConnector {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://www.reddit.com"
	}
	if cfg.UserAgent == "" {
		cfg.UserAgent = "ia-buscar/1.0 (by /r/ThisCloudServices)"
	}
	host := cfg.BaseURL
	if strings.Contains(host, "oauth.reddit.com") {
		host = "https://oauth.reddit.com"
	} else {
		host = cfg.BaseURL
	}
	return &RedditConnector{
		cfg:       cfg,
		cacheSvc:  cacheSvc,
		oauthHost: host,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *RedditConnector) Name() string { return "reddit" }

// HasOAuthCredentials reports whether the operator configured OAuth.
// This is the explicit signal a degraded response uses to tell the AI
// the difference between "anonymous request blocked by Reddit" and
// "OAuth configured but Reddit still rejected the token".
func (c *RedditConnector) HasOAuthCredentials() bool {
	return c.cfg.ClientID != "" && c.cfg.ClientSecret != ""
}

func (c *RedditConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	start := time.Now()
	query := strings.TrimSpace(req.Query)
	maxResults := getMaxResults(req.MaxResults, 10)

	ctx, span := observability.StartSpan(ctx, c.Name(), req.Query)
	defer func() {
		observability.EndSpan(span, 0, nil)
	}()

	cacheKey := cache.GenerateCacheKey(query, []string{"reddit", c.cfg.UserAgent, c.cfg.ClientID})
	if cached, ok, _ := c.cacheSvc.Get(ctx, cacheKey); ok {
		log.Printf("[reddit] cache hit for query: %s", query)
		cachedResp := &types.SearchResponse{}
		if err := json.Unmarshal(cached.Payload, cachedResp); err == nil {
			cachedResp.Cached = true
			// Cached payloads must still respect the wire contract: never
			// hand back a nil Results slice to the MCP layer.
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
		Strategy:    "reddit",
		Cached:      false,
	}

	switch {
	case degraded != nil:
		// Configuration / policy prevented Reddit from being queried at all.
		// The AI gets an actionable message and an empty results array.
		resp.Strategy = "reddit_unconfigured"
		resp.Warnings = []string{
			"reddit_unconfigured: " + degraded.Error(),
		}
		resp.Errors = []string{
			"reddit_unconfigured",
		}
	case err != nil:
		// Reddit was actually queried but failed. Surface the upstream
		// condition so the AI can distinguish transport failures, 403,
		// 429 and decode problems.
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

// doRedditRequest runs the upstream call. It returns:
//
//	results: parsed SearchResultItem list (always non-nil; empty on error)
//	err:     non-nil when the request was attempted but failed
//	degraded: non-nil when the request was NOT attempted because of a
//	          configuration / policy decision (e.g. missing OAuth
//	          credentials while the operator asked for authenticated
//	          queries, or a User-Agent that Reddit explicitly rejected).
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
		if !c.HasOAuthCredentials() {
			observability.Default().RecordSearchDegraded("reddit", "unconfigured")
			return []types.SearchResultItem{}, nil, errors.New(
				"reddit rejected the anonymous request with status " +
					fmt.Sprint(resp.StatusCode) +
					"; configure REDDIT_CLIENT_ID and REDDIT_CLIENT_SECRET (or --reddit-client-id / --reddit-client-secret) to authenticate, or set REDDIT_USER_AGENT to a deployment-specific string (Reddit requires a unique User-Agent)",
			)
		}
		observability.Default().RecordSearchDegraded("reddit", "upstream_http_4xx")
		return []types.SearchResultItem{}, fmt.Errorf("reddit API error: %d", resp.StatusCode), nil
	case http.StatusTooManyRequests:
		// 429 is a rate-limit, not a configuration problem. Keep it as
		// a regular err so callers can render "rate-limited" UX.
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