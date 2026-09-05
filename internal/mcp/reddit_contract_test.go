package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/connectors"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/memory"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

func newRedditTestServer(t *testing.T, upstream http.Handler) *Server {
	t.Helper()
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewRedditConnector(connectors.RedditConfig{SearxngURL: srv.URL}, cacheSvc))
	return NewServer(cm, search.NewPlanner(), "stdio", ":8080", srv.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10), memory.NewClient("", ""))
}

func TestRedditSearxngFailureIsPartial(t *testing.T) {
	s := newRedditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	resp, err := s.callToolByName(context.Background(), "search_reddit", []byte(`{"query":"provider-failure"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Strategy != "searxng_reddit_index" || !resp.Partial {
		t.Fatalf("expected a partial SearXNG response, got strategy=%q partial=%v warnings=%v", resp.Strategy, resp.Partial, resp.Warnings)
	}
	if len(resp.Warnings) == 0 || !strings.Contains(resp.Warnings[0], "searxng error: 503") {
		t.Fatalf("expected the SearXNG failure warning, got %v", resp.Warnings)
	}
}

func TestRedditSearxngEmptyResultIsNotPartial(t *testing.T) {
	s := newRedditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))

	resp, err := s.callToolByName(context.Background(), "search_reddit", []byte(`{"query":"genuine-empty"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Partial || len(resp.Results) != 0 || resp.Strategy != "searxng_reddit_index" {
		t.Fatalf("expected a genuine empty SearXNG result, got strategy=%q partial=%v results=%d", resp.Strategy, resp.Partial, len(resp.Results))
	}
}

func TestRedditSearxngFailureSerializesAnEmptyArray(t *testing.T) {
	s := newRedditTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))

	resp, err := s.callToolByName(context.Background(), "search_reddit", []byte(`{"query":"degraded-wire"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"results":[]`) {
		t.Errorf("degraded path must serialize results:[]; got %s", raw)
	}
}
