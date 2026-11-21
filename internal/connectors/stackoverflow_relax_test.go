package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

const soItemJSON = `{"items":[{"question_id":7,"title":"t","link":"https://stackoverflow.com/q/7","score":3,"answer_count":1,"tags":["go"],"owner":{"display_name":"o"},"creation_date":1700000000}]}`

func newRelaxStackOverflow(t *testing.T, up *relaxUpstream) (*StackOverflowConnector, *cache.Service) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(up.handler))
	t.Cleanup(srv.Close)
	svc := cache.NewService(300)
	c := NewStackOverflowConnector(svc)
	c.baseURL = srv.URL
	return c, svc
}

func soKey(q string) string {
	return cache.GenerateCacheKey(strings.TrimSpace(q), []string{"stackoverflow"}, "")
}

func TestStackOverflowRelaxesQueryOnZeroResults(t *testing.T) {
	up := &relaxUpstream{bodies: []string{`{"items":[]}`, soItemJSON}}
	c, svc := newRelaxStackOverflow(t, up)
	q := "how to implement the retry logic in golang http client"
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	want := "implement retry logic golang"
	seen := up.seen()
	if len(seen) != 2 || seen[0] != q || seen[1] != want {
		t.Fatalf("queries=%q", seen)
	}
	if resp.Query != q || len(resp.Results) != 1 {
		t.Fatalf("resp=%+v", resp)
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0] != "query relaxed: "+want {
		t.Fatalf("warnings=%q", resp.Warnings)
	}
	if ttl := cachedTTL(t, svc, soKey(q)); ttl != 300*time.Second {
		t.Fatalf("non-empty final TTL=%v want 300s", ttl)
	}
	again, _ := c.Search(context.Background(), &types.SearchRequest{Query: q})
	if !again.Cached || len(up.seen()) != 2 {
		t.Fatalf("second call cached=%v requests=%d", again.Cached, len(up.seen()))
	}
}

func TestStackOverflowEmptyCachedAtMost30s(t *testing.T) {
	for _, q := range []string{"how to implement the retry logic in golang http client", "golang retry"} {
		t.Run(q, func(t *testing.T) {
			up := &relaxUpstream{}
			c, svc := newRelaxStackOverflow(t, up)
			resp, err := c.Search(context.Background(), &types.SearchRequest{Query: q})
			if err != nil || len(resp.Results) != 0 {
				t.Fatalf("resp=%+v err=%v", resp, err)
			}
			if ttl := cachedTTL(t, svc, soKey(q)); ttl > DegradedSearchCacheTTL {
				t.Fatalf("empty TTL=%v exceeds %v", ttl, DegradedSearchCacheTTL)
			}
		})
	}
}

func TestStackOverflowNoRelaxAfterError(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadRequest} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			up := &relaxUpstream{statuses: []int{status}}
			c, _ := newRelaxStackOverflow(t, up)
			resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "how to implement the retry logic in golang"})
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

func TestStackOverflowNoRelaxWhenQueryCannotBeShortened(t *testing.T) {
	for _, q := range []string{"golang retry", "tag:go", "the and of"} {
		t.Run(q, func(t *testing.T) {
			up := &relaxUpstream{}
			c, _ := newRelaxStackOverflow(t, up)
			resp, err := c.Search(context.Background(), &types.SearchRequest{Query: q})
			if err != nil {
				t.Fatal(err)
			}
			if n := len(up.seen()); n != 1 || len(resp.Warnings) != 0 {
				t.Fatalf("requests=%d warnings=%q", n, resp.Warnings)
			}
		})
	}
}

func TestStackOverflowNonEmptyFirstResponseUnchanged(t *testing.T) {
	up := &relaxUpstream{bodies: []string{soItemJSON}}
	c, svc := newRelaxStackOverflow(t, up)
	q := "how to implement the retry logic in golang http client"
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: q})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(up.seen()); n != 1 {
		t.Fatalf("upstream requests=%d want 1", n)
	}
	if len(resp.Results) != 1 || resp.Query != q || len(resp.Warnings) != 0 || resp.Partial {
		t.Fatalf("resp=%+v", resp)
	}
	if ttl := cachedTTL(t, svc, soKey(q)); ttl != 300*time.Second {
		t.Fatalf("TTL=%v want 300s", ttl)
	}
}
