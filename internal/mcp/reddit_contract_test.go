package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/connectors"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

// TestRedditConfigurationDegradation_AnonymousRejected covers the
// "configuration / policy prevents Reddit search" path from item 2:
// when the connector is not configured with OAuth credentials and
// Reddit rejects the anonymous request with 403, the MCP response must
// surface Strategy="reddit_unconfigured" with an actionable warning
// naming REDDIT_CLIENT_ID / REDDIT_CLIENT_SECRET / REDDIT_USER_AGENT.
func TestRedditConfigurationDegradation_AnonymousRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewRedditConnector(connectors.RedditConfig{
		BaseURL:   srv.URL,
		UserAgent: "ia-buscar/test",
	}, cacheSvc))
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New())

	resp, err := s.callToolByName(context.Background(), "search_reddit", []byte(`{"query":"config-anon-403"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Strategy != "reddit_unconfigured" {
		t.Errorf("expected Strategy=reddit_unconfigured, got %q", resp.Strategy)
	}
	if resp.Partial {
		t.Errorf("expected Partial=false (config issue, not partial result), got true (warnings=%v)", resp.Warnings)
	}
	if len(resp.Results) != 0 {
		t.Errorf("expected empty Results, got %d", len(resp.Results))
	}
	if len(resp.Warnings) == 0 {
		t.Fatal("expected at least one actionable warning")
	}
	joined := strings.ToLower(strings.Join(resp.Warnings, " | "))
	for _, mustHave := range []string{"reddit_unconfigured", "reddit_client_id", "reddit_client_secret", "reddit_user_agent"} {
		if !strings.Contains(joined, mustHave) {
			t.Errorf("expected warning to mention %q, got %v", mustHave, resp.Warnings)
		}
	}
}

// TestRedditConfigurationDegradation_OAuthStillRejected covers the
// "OAuth configured but Reddit still rejected" path: the connector
// must report this as upstream degradation (Partial=true), NOT as a
// configuration problem.
func TestRedditConfigurationDegradation_OAuthStillRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewRedditConnector(connectors.RedditConfig{
		BaseURL:      srv.URL,
		UserAgent:    "ia-buscar/test",
		ClientID:     "configured",
		ClientSecret: "configured",
	}, cacheSvc))
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New())

	resp, err := s.callToolByName(context.Background(), "search_reddit", []byte(`{"query":"config-oauth-403"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Strategy == "reddit_unconfigured" {
		t.Errorf("must NOT classify as unconfigured when OAuth is set, got %q", resp.Strategy)
	}
	if !resp.Partial {
		t.Errorf("expected Partial=true when OAuth is configured but Reddit still rejected, got false (warnings=%v)", resp.Warnings)
	}
}

// TestRedditRateLimitIsPartial verifies that 429 (rate-limited) stays a
// regular upstream degradation, never a configuration problem.
func TestRedditRateLimitIsPartial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewRedditConnector(connectors.RedditConfig{BaseURL: srv.URL, UserAgent: "ia-buscar/test"}, cacheSvc))
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New())

	resp, err := s.callToolByName(context.Background(), "search_reddit", []byte(`{"query":"rate-limited"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Partial {
		t.Errorf("expected Partial=true on 429, got false (warnings=%v)", resp.Warnings)
	}
	if resp.Strategy == "reddit_unconfigured" {
		t.Errorf("429 must NOT be classified as reddit_unconfigured, got %q", resp.Strategy)
	}
}

// TestRedditUserAgentIsPluggable proves the User-Agent header the
// connector actually sends is the one supplied through the
// configuration seam, not a hard-coded literal.
func TestRedditUserAgentIsPluggable(t *testing.T) {
	const customUA = "ia-buscar/test-custom-ua/42"

	gotUA := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA <- r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":{"children":[]}}`))
	}))
	defer srv.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewRedditConnector(connectors.RedditConfig{
		BaseURL:   srv.URL,
		UserAgent: customUA,
	}, cacheSvc))
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New())

	if _, err := s.callToolByName(context.Background(), "search_reddit", []byte(`{"query":"ua-pluggable"}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case ua := <-gotUA:
		if ua != customUA {
			t.Errorf("expected User-Agent %q, got %q", customUA, ua)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for upstream request")
	}
}

// TestRedditStableEmptyArrayOnDegradedResponse covers the wire
// contract for the degraded path: even when Reddit was not configured
// or returned an error, the AI still gets an empty results array, never
// null/omitted. The path runs through the MCP boundary to prove the
// normalization helper handles degraded responses too.
func TestRedditStableEmptyArrayOnDegradedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewRedditConnector(connectors.RedditConfig{BaseURL: srv.URL}, cacheSvc))
	s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New())

	resp, err := s.callToolByName(context.Background(), "search_reddit", []byte(`{"query":"degraded-wire"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"results":[]`) {
		t.Errorf("degraded path must still serialize results:[]; got %s", raw)
	}
	if strings.Contains(string(raw), `"results":null`) {
		t.Errorf("degraded path must never serialize results:null; got %s", raw)
	}
}