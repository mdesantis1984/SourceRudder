package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

func TestSearxNGConnectorsPreserveResultsWithUnresponsiveEngines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resultURL := "https://www.youtube.com/watch?v=1234"
		if strings.HasSuffix(r.URL.Query().Get("q"), "academic") {
			resultURL = "https://arxiv.org/abs/1234"
		}
		body := `{"results":[{"title":"kept result","url":"` + resultURL + `","content":"snippet","source":"source","engine":"healthy-engine","parsed_url":{"domain":"result.example"}}],"unresponsive_engines":[["failed-engine","timeout"]]}`
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	connectors := []struct {
		name string
		new  func(*cache.Service) types.SearchConnector
	}{
		{"web", func(cacheSvc *cache.Service) types.SearchConnector { return NewWebConnector(srv.URL, cacheSvc) }},
		{"news", func(cacheSvc *cache.Service) types.SearchConnector { return NewNewsConnector(srv.URL, cacheSvc) }},
		{"youtube", func(cacheSvc *cache.Service) types.SearchConnector { return NewYouTubeConnector(srv.URL, cacheSvc) }},
		{"images", func(cacheSvc *cache.Service) types.SearchConnector { return NewImagesConnector(srv.URL, cacheSvc) }},
		{"academic", func(cacheSvc *cache.Service) types.SearchConnector { return NewAcademicConnector(srv.URL, cacheSvc) }},
	}

	for _, tc := range connectors {
		t.Run(tc.name, func(t *testing.T) {
			met := observability.New()
			observability.SetDefault(met)
			resp, err := tc.new(cache.NewService(60)).Search(context.Background(), &types.SearchRequest{Query: "partial-" + tc.name})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Results) != 1 || resp.Results[0].Title != "kept result" {
				t.Fatalf("expected useful result to be preserved, got %#v", resp.Results)
			}
			if !resp.Partial {
				t.Fatal("expected Partial=true when an engine is unresponsive")
			}
			if len(resp.Warnings) != 1 || !strings.Contains(resp.Warnings[0], "failed-engine") {
				t.Fatalf("expected warning naming failed-engine, got %#v", resp.Warnings)
			}
			if len(resp.Errors) != 0 {
				t.Fatalf("useful partial results must not be total failures: errors=%v", resp.Errors)
			}
			if got := readDegradedCounter(t, met, tc.name, "unresponsive_engines"); got != 1 {
				t.Fatalf("expected one degradation metric, got %v", got)
			}
		})
	}
}

func TestSearxNGConnectorsPreserveCachedPartialResponses(t *testing.T) {
	const body = `{"results":[{"title":"kept result","url":"https://www.youtube.com/watch?v=1234","content":"snippet","source":"source","engine":"healthy-engine","parsed_url":{"domain":"result.example"}}],"unresponsive_engines":[["failed-engine","timeout"]]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		response := body
		if strings.HasSuffix(r.URL.Query().Get("q"), "academic") {
			response = strings.Replace(body, "https://www.youtube.com/watch?v=1234", "https://arxiv.org/abs/1234", 1)
		}
		_, _ = w.Write([]byte(response))
	}))
	defer srv.Close()

	connectors := []struct {
		name string
		new  func(*cache.Service) types.SearchConnector
	}{
		{"web", func(cacheSvc *cache.Service) types.SearchConnector { return NewWebConnector(srv.URL, cacheSvc) }},
		{"news", func(cacheSvc *cache.Service) types.SearchConnector { return NewNewsConnector(srv.URL, cacheSvc) }},
		{"youtube", func(cacheSvc *cache.Service) types.SearchConnector { return NewYouTubeConnector(srv.URL, cacheSvc) }},
		{"images", func(cacheSvc *cache.Service) types.SearchConnector { return NewImagesConnector(srv.URL, cacheSvc) }},
		{"academic", func(cacheSvc *cache.Service) types.SearchConnector { return NewAcademicConnector(srv.URL, cacheSvc) }},
	}

	for _, tc := range connectors {
		t.Run(tc.name, func(t *testing.T) {
			connector := tc.new(cache.NewService(60))
			request := &types.SearchRequest{Query: "cached-partial-" + tc.name}
			first, err := connector.Search(context.Background(), request)
			if err != nil {
				t.Fatalf("uncached search: %v", err)
			}
			cached, err := connector.Search(context.Background(), request)
			if err != nil {
				t.Fatalf("cached search: %v", err)
			}
			if !first.Partial || len(first.Results) != 1 || len(first.Warnings) != 1 || len(first.Errors) != 0 {
				t.Fatalf("uncached response lost partial metadata: %#v", first)
			}
			if !cached.Cached || !cached.Partial || len(cached.Results) != 1 || cached.Results[0].Title != "kept result" {
				t.Fatalf("cached response did not preserve result metadata: %#v", cached)
			}
			if len(cached.Warnings) != 1 || !strings.Contains(cached.Warnings[0], "failed-engine") {
				t.Fatalf("cached response did not preserve warning: %#v", cached.Warnings)
			}
			if len(cached.Errors) != 0 {
				t.Fatalf("cached partial response must preserve empty Errors, got %v", cached.Errors)
			}
		})
	}
}
