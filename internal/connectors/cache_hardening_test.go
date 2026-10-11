package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func TestSearxNGCacheHardening(t *testing.T) {
	for _, name := range []string{"web", "news", "youtube", "images", "academic", "reddit"} {
		t.Run(name, func(t *testing.T) {
			degraded := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				resultURL := "https://www.youtube.com/watch?v=1234"
				if name == "academic" {
					resultURL = "https://arxiv.org/abs/1234"
				}
				if name == "reddit" {
					resultURL = "https://www.reddit.com/r/golang/comments/1234/example/"
				}
				warning := "[]"
				if degraded {
					warning = `[["brave","429"]]`
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"results":[{"title":"kept","url":"` + resultURL + `","img_src":"https://example.test/image.jpg","content":"snippet","engine":"bing"}],"unresponsive_engines":` + warning + `}`))
			}))
			defer srv.Close()
			svc := cache.NewService(300)
			var connector types.SearchConnector
			switch name {
			case "web":
				connector = NewWebConnector(srv.URL, svc)
			case "news":
				connector = NewNewsConnector(srv.URL, svc)
			case "youtube":
				connector = NewYouTubeConnector(srv.URL, svc)
			case "images":
				connector = NewImagesConnector(srv.URL, svc)
			case "academic":
				connector = NewAcademicConnector(srv.URL, svc)
			case "reddit":
				connector = NewRedditConnector(RedditConfig{SearxngURL: srv.URL}, svc)
			}
			ctx := context.Background()
			t.Run("provenance", func(t *testing.T) {
				resp, err := connector.Search(ctx, &types.SearchRequest{Query: "provenance"})
				if err != nil || len(resp.Results) != 1 {
					t.Fatalf("response=%+v err=%v", resp, err)
				}
				wire, _ := json.Marshal(resp.Results[0])
				if !strings.Contains(string(wire), `"engine":"bing"`) {
					t.Fatalf("missing engine: %s", wire)
				}
				if name == "web" && resp.Results[0].Source != "searxng" {
					t.Fatal("source contract changed")
				}
			})
			t.Run("identity", func(t *testing.T) {
				for _, req := range []*types.SearchRequest{
					{Query: "identity"}, {Query: "identity", Language: "en"}, {Query: "identity", SafeSearch: true},
					{Query: "identity", MaxResults: 1}, {Query: "identity", TimeRange: "day"},
				} {
					resp, err := connector.Search(ctx, req)
					if err != nil || resp.Cached {
						t.Errorf("request %+v collided: err=%v cached=%v", req, err, resp.Cached)
					}
				}
			})
			for _, partial := range []bool{false, true} {
				label := "healthy"
				if partial {
					label = "degraded"
				}
				t.Run(label, func(t *testing.T) {
					svc.Clear(ctx)
					degraded = partial
					req := &types.SearchRequest{Query: label}
					resp, err := connector.Search(ctx, req)
					if err != nil || resp.Partial != partial {
						t.Fatalf("response=%+v err=%v", resp, err)
					}
					keys, _ := svc.Keys(ctx)
					if len(keys) != 1 {
						t.Fatalf("expected one entry: %v", keys)
					}
					entry, _, _ := svc.Get(ctx, keys[0])
					ttl := entry.ExpiresAt.Sub(entry.CreatedAt)
					if partial && ttl > 30*time.Second {
						t.Errorf("degraded TTL=%v exceeds 30s", ttl)
					}
					if !partial && ttl != 300*time.Second {
						t.Errorf("healthy TTL=%v", ttl)
					}
					again, err := connector.Search(ctx, req)
					if err != nil || !again.Cached || again.Partial != partial {
						t.Fatalf("cached response=%+v err=%v", again, err)
					}
				})
			}
		})
	}
}
