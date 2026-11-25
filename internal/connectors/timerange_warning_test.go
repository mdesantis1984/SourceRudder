package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

const wantTimeRangeWarning = "timeRange: SearXNG skips engines without date filtering; retry without timeRange for undated results"

type searxngConnectorCase struct {
	name string
	run  func(url string, req *types.SearchRequest) (*types.SearchResponse, error)
}

func timeRangeConnectors() []searxngConnectorCase {
	ctx := context.Background()
	svc := cache.NewService(300)
	return []searxngConnectorCase{
		{"web", func(u string, r *types.SearchRequest) (*types.SearchResponse, error) {
			return NewWebConnector(u, svc).Search(ctx, r)
		}},
		{"news", func(u string, r *types.SearchRequest) (*types.SearchResponse, error) {
			return NewNewsConnector(u, svc).Search(ctx, r)
		}},
		{"images", func(u string, r *types.SearchRequest) (*types.SearchResponse, error) {
			return NewImagesConnector(u, svc).Search(ctx, r)
		}},
		{"academic", func(u string, r *types.SearchRequest) (*types.SearchResponse, error) {
			return NewAcademicConnector(u, svc).Search(ctx, r)
		}},
		{"youtube", func(u string, r *types.SearchRequest) (*types.SearchResponse, error) {
			return NewYouTubeConnector(u, svc).Search(ctx, r)
		}},
		{"reddit", func(u string, r *types.SearchRequest) (*types.SearchResponse, error) {
			return NewRedditConnector(RedditConfig{SearxngURL: u}, svc).Search(ctx, r)
		}},
	}
}

func TestTimeRangeEmptyWarning(t *testing.T) {
	var hits atomic.Int32
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
	}))
	defer empty.Close()
	full := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"t","url":"https://arxiv.org/abs/1","content":"c","engine":"bing","img_src":"https://img.example/a.jpg","thumbnail_src":"https://img.example/a.jpg","parsed_url":["https","arxiv.org","/abs/1","","",""]}],"unresponsive_engines":[]}`))
	}))
	defer full.Close()
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer broken.Close()

	for _, tc := range timeRangeConnectors() {
		t.Run(tc.name+"/empty with timeRange warns", func(t *testing.T) {
			hits.Store(0)
			resp, err := tc.run(empty.URL, &types.SearchRequest{Query: "tr-empty-" + tc.name, TimeRange: "day"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !slices.Contains(resp.Warnings, wantTimeRangeWarning) {
				t.Fatalf("warnings = %q, want %q", resp.Warnings, wantTimeRangeWarning)
			}
			if resp.Partial {
				t.Errorf("an empty healthy response must stay Partial=false")
			}
			n := 0
			for _, w := range resp.Warnings {
				if w == wantTimeRangeWarning {
					n++
				}
			}
			if n != 1 {
				t.Errorf("warning appears %d times, want once", n)
			}
		})
		t.Run(tc.name+"/empty without timeRange stays silent", func(t *testing.T) {
			resp, err := tc.run(empty.URL, &types.SearchRequest{Query: "tr-notr-" + tc.name})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Warnings) != 0 {
				t.Errorf("warnings = %q, want none", resp.Warnings)
			}
		})
		t.Run(tc.name+"/results with timeRange stay silent", func(t *testing.T) {
			resp, err := tc.run(full.URL, &types.SearchRequest{Query: "tr-full-" + tc.name, TimeRange: "week"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(resp.Results) == 0 {
				t.Skip("connector filtered the fixture result; covered by the other cases")
			}
			if slices.Contains(resp.Warnings, wantTimeRangeWarning) {
				t.Errorf("warning must not appear when results exist: %q", resp.Warnings)
			}
		})
		t.Run(tc.name+"/hard error does not blame timeRange", func(t *testing.T) {
			resp, err := tc.run(broken.URL, &types.SearchRequest{Query: "tr-broken-" + tc.name, TimeRange: "month"})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !resp.Partial {
				t.Errorf("expected Partial=true on HTTP 500")
			}
			if slices.Contains(resp.Warnings, wantTimeRangeWarning) {
				t.Errorf("timeRange warning must not mask a hard error: %q", resp.Warnings)
			}
		})
	}
}

func TestTimeRangeEmptyWarningIsNotRetried(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Query().Get("time_range") == "" {
			t.Errorf("request without time_range: the connector must not retry without it")
		}
		_, _ = w.Write([]byte(`{"results":[],"unresponsive_engines":[]}`))
	}))
	defer srv.Close()
	_, err := NewNewsConnector(srv.URL, cache.NewService(300)).Search(context.Background(),
		&types.SearchRequest{Query: "tr-noretry", TimeRange: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("SearXNG hits = %d, want 1", hits.Load())
	}
}
