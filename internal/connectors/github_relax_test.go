package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

const relaxLongQuery = "how to implement the retry logic in golang http client repo:foo/bar"

type relaxUpstream struct {
	mu       sync.Mutex
	queries  []string
	statuses []int // per request status; 0 means 200
	bodies   []string
}

func (u *relaxUpstream) handler(w http.ResponseWriter, r *http.Request) {
	u.mu.Lock()
	idx := len(u.queries)
	u.queries = append(u.queries, r.URL.Query().Get("q"))
	status, body := 0, `{"items":[]}`
	if idx < len(u.statuses) {
		status = u.statuses[idx]
	}
	if idx < len(u.bodies) {
		body = u.bodies[idx]
	}
	u.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if status != 0 {
		w.WriteHeader(status)
	}
	_, _ = w.Write([]byte(body))
}

func (u *relaxUpstream) seen() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.queries...)
}

type githubRelaxMode struct {
	name     string
	tags     []string
	suffix   string
	search   func(c *GitHubConnector, ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error)
	itemJSON string
}

func githubRelaxModes() []githubRelaxMode {
	return []githubRelaxMode{
		{"repositories", []string{"github", "repo"}, "", func(c *GitHubConnector, ctx context.Context, r *types.SearchRequest) (*types.SearchResponse, error) {
			return c.Search(ctx, r)
		}, `{"items":[{"full_name":"foo/bar","html_url":"https://github.com/foo/bar","description":"d","owner":{"login":"foo"}}]}`},
		{"pull requests", []string{"github", "pr"}, " is:pr", func(c *GitHubConnector, ctx context.Context, r *types.SearchRequest) (*types.SearchResponse, error) {
			return c.SearchPR(ctx, r)
		}, `{"items":[{"title":"fix","html_url":"https://github.com/foo/bar/pull/1","owner":{"login":"foo"}}]}`},
		{"issues", []string{"github", "issue"}, " is:issue", func(c *GitHubConnector, ctx context.Context, r *types.SearchRequest) (*types.SearchResponse, error) {
			return c.SearchIssue(ctx, r)
		}, `{"items":[{"title":"bug","html_url":"https://github.com/foo/bar/issues/1","owner":{"login":"foo"}}]}`},
	}
}

func newRelaxGitHub(t *testing.T, up *relaxUpstream) (*GitHubConnector, *cache.Service) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	svc := cache.NewService(300)
	c := NewGitHubConnector("", svc)
	c.baseURL = srv.URL
	return c, svc
}

func cachedTTL(t *testing.T, svc *cache.Service, key string) time.Duration {
	t.Helper()
	entry, ok, _ := svc.Get(context.Background(), key)
	if !ok {
		t.Fatalf("expected cache entry for key %q", key)
	}
	return entry.ExpiresAt.Sub(entry.CreatedAt)
}

func TestGitHubRelaxesQueryOnZeroResults(t *testing.T) {
	for _, m := range githubRelaxModes() {
		t.Run(m.name, func(t *testing.T) {
			up := &relaxUpstream{bodies: []string{`{"items":[]}`, m.itemJSON}}
			c, svc := newRelaxGitHub(t, up)
			resp, err := m.search(c, context.Background(), &types.SearchRequest{Query: relaxLongQuery})
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			want := "implement retry logic golang repo:foo/bar"
			seen := up.seen()
			if len(seen) != 2 {
				t.Fatalf("upstream requests=%d want 2: %v", len(seen), seen)
			}
			if seen[0] != relaxLongQuery+m.suffix || seen[1] != want+m.suffix {
				t.Fatalf("queries=%q", seen)
			}
			if resp.Query != relaxLongQuery {
				t.Fatalf("query=%q want original", resp.Query)
			}
			if len(resp.Results) != 1 {
				t.Fatalf("results=%d want 1", len(resp.Results))
			}
			if len(resp.Warnings) != 1 || resp.Warnings[0] != "query relaxed: "+want {
				t.Fatalf("warnings=%q", resp.Warnings)
			}
			key := cache.GenerateCacheKey(sanitizeQuery(relaxLongQuery), m.tags, "")
			if ttl := cachedTTL(t, svc, key); ttl != 300*time.Second {
				t.Fatalf("non-empty final TTL=%v want 300s", ttl)
			}
			again, _ := m.search(c, context.Background(), &types.SearchRequest{Query: relaxLongQuery})
			if !again.Cached || len(up.seen()) != 2 {
				t.Fatalf("second call cached=%v requests=%d", again.Cached, len(up.seen()))
			}
		})
	}
}

func TestGitHubEmptyAfterRelaxCachedAtMost30s(t *testing.T) {
	for _, m := range githubRelaxModes() {
		t.Run(m.name, func(t *testing.T) {
			up := &relaxUpstream{}
			c, svc := newRelaxGitHub(t, up)
			resp, err := m.search(c, context.Background(), &types.SearchRequest{Query: relaxLongQuery})
			if err != nil || len(resp.Results) != 0 {
				t.Fatalf("resp=%+v err=%v", resp, err)
			}
			if n := len(up.seen()); n != 2 {
				t.Fatalf("upstream requests=%d want 2", n)
			}
			if len(resp.Warnings) != 1 || !strings.HasPrefix(resp.Warnings[0], "query relaxed: ") {
				t.Fatalf("warnings=%q", resp.Warnings)
			}
			key := cache.GenerateCacheKey(sanitizeQuery(relaxLongQuery), m.tags, "")
			if ttl := cachedTTL(t, svc, key); ttl > DegradedSearchCacheTTL {
				t.Fatalf("empty TTL=%v exceeds %v", ttl, DegradedSearchCacheTTL)
			}
		})
	}
}

func TestGitHubEmptyWithoutRetryCachedAtMost30s(t *testing.T) {
	for _, m := range githubRelaxModes() {
		t.Run(m.name, func(t *testing.T) {
			up := &relaxUpstream{}
			c, svc := newRelaxGitHub(t, up)
			if _, err := m.search(c, context.Background(), &types.SearchRequest{Query: "golang retry"}); err != nil {
				t.Fatal(err)
			}
			key := cache.GenerateCacheKey(sanitizeQuery("golang retry"), m.tags, "")
			if ttl := cachedTTL(t, svc, key); ttl > DegradedSearchCacheTTL {
				t.Fatalf("empty TTL=%v exceeds %v", ttl, DegradedSearchCacheTTL)
			}
		})
	}
}

func TestGitHubNoRelaxAfterError(t *testing.T) {
	for _, m := range githubRelaxModes() {
		for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusUnprocessableEntity} {
			t.Run(m.name+"/"+http.StatusText(status), func(t *testing.T) {
				up := &relaxUpstream{statuses: []int{status}}
				c, _ := newRelaxGitHub(t, up)
				resp, err := m.search(c, context.Background(), &types.SearchRequest{Query: relaxLongQuery})
				if err != nil {
					t.Fatal(err)
				}
				if n := len(up.seen()); n != 1 {
					t.Fatalf("upstream requests=%d want 1", n)
				}
				for _, w := range resp.Warnings {
					if strings.HasPrefix(w, "query relaxed") {
						t.Fatalf("unexpected relax warning %q", w)
					}
				}
				if !resp.Partial {
					t.Fatal("error response must stay partial")
				}
			})
		}
	}
}

func TestGitHubNoRelaxWhenQueryCannotBeShortened(t *testing.T) {
	for _, q := range []string{"golang retry", "repo:foo/bar", "the and of"} {
		for _, m := range githubRelaxModes() {
			t.Run(m.name+"/"+q, func(t *testing.T) {
				up := &relaxUpstream{}
				c, _ := newRelaxGitHub(t, up)
				resp, err := m.search(c, context.Background(), &types.SearchRequest{Query: q})
				if err != nil {
					t.Fatal(err)
				}
				if n := len(up.seen()); n != 1 {
					t.Fatalf("upstream requests=%d want 1", n)
				}
				if len(resp.Warnings) != 0 {
					t.Fatalf("warnings=%q", resp.Warnings)
				}
			})
		}
	}
}

func TestGitHubNonEmptyFirstResponseUnchanged(t *testing.T) {
	for _, m := range githubRelaxModes() {
		t.Run(m.name, func(t *testing.T) {
			up := &relaxUpstream{bodies: []string{m.itemJSON}}
			c, svc := newRelaxGitHub(t, up)
			resp, err := m.search(c, context.Background(), &types.SearchRequest{Query: relaxLongQuery})
			if err != nil {
				t.Fatal(err)
			}
			if n := len(up.seen()); n != 1 {
				t.Fatalf("upstream requests=%d want 1", n)
			}
			if len(resp.Results) != 1 || resp.Query != relaxLongQuery || len(resp.Warnings) != 0 || resp.Partial {
				t.Fatalf("resp=%+v", resp)
			}
			key := cache.GenerateCacheKey(sanitizeQuery(relaxLongQuery), m.tags, "")
			if ttl := cachedTTL(t, svc, key); ttl != 300*time.Second {
				t.Fatalf("TTL=%v want 300s", ttl)
			}
		})
	}
}
