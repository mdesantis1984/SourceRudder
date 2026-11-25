package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func init() {
	// Keep the suite fast; TestRetryWaitsTheBackoffAndHonoursContext sets its own.
	retryBackoff = time.Millisecond
}

// flakyServer answers with the given statuses in order (0 = serve ok), then ok.
type flakyServer struct {
	*httptest.Server
	hits     atomic.Int32
	statuses []int
}

func newFlakyServer(t *testing.T, ok func(w http.ResponseWriter), statuses ...int) *flakyServer {
	t.Helper()
	f := &flakyServer{statuses: statuses}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(f.hits.Add(1)) - 1
		if n < len(f.statuses) && f.statuses[n] != 0 {
			http.Error(w, "flaky", f.statuses[n])
			return
		}
		ok(w)
	}))
	t.Cleanup(f.Close)
	return f
}

func hnOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(hnJSON(hnHit("1", "Kubernetes retry story", `"https://example.com/k8s"`, "a", 10, 2, techNow.Add(-time.Hour)))))
}

func gnOK(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/rss+xml")
	_, _ = w.Write([]byte(rssAt([3]string{"Kubernetes news - Wire", "https://news.example.com/k", techNow.Add(-time.Hour).Format(time.RFC1123Z)})))
}

func techConnector(hn, gn string) *TechNewsConnector {
	return NewTechNewsConnector(cache.NewService(300), TechNewsConfig{HNURL: hn, GoogleNewsURL: gn, Feeds: []TechFeed{}, Now: func() time.Time { return techNow }})
}

func TestHNAlgoliaRetriesOnceOnServerErrorAndRateLimit(t *testing.T) {
	for _, status := range []int{500, 502, 503, 429} {
		hn := newFlakyServer(t, hnOK, status)
		gn := newFlakyServer(t, func(w http.ResponseWriter) { http.Error(w, "no", 404) })
		c := techConnector(hn.URL, gn.URL)
		cands, err := c.searchHN(context.Background(), "kubernetes", "", techNow)
		if err != nil || len(cands) != 1 {
			t.Errorf("status %d: cands=%d err=%v, want the retry to succeed", status, len(cands), err)
		}
		if hn.hits.Load() != 2 {
			t.Errorf("status %d: hits=%d want 2", status, hn.hits.Load())
		}
	}
}

func TestHNAlgoliaGivesUpAfterOneRetry(t *testing.T) {
	hn := newFlakyServer(t, hnOK, 503, 503, 503)
	c := techConnector(hn.URL, "http://127.0.0.1:1")
	_, err := c.searchHN(context.Background(), "kubernetes", "", techNow)
	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err=%v want an HTTP 503 error", err)
	}
	if hn.hits.Load() != 2 {
		t.Fatalf("hits=%d want exactly 2 (one retry)", hn.hits.Load())
	}
}

func TestHNAlgoliaDoesNotRetryOtherClientErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404} {
		hn := newFlakyServer(t, hnOK, status)
		c := techConnector(hn.URL, "http://127.0.0.1:1")
		if _, err := c.searchHN(context.Background(), "kubernetes", "", techNow); err == nil {
			t.Errorf("status %d: want an error", status)
		}
		if hn.hits.Load() != 1 {
			t.Errorf("status %d: hits=%d want 1 (no retry)", status, hn.hits.Load())
		}
	}
}

func TestHNAlgoliaRetriesNetworkErrors(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close() // connection reset before any response
			return
		}
		hnOK(w)
	}))
	defer srv.Close()
	c := techConnector(srv.URL, "http://127.0.0.1:1")
	cands, err := c.searchHN(context.Background(), "kubernetes", "", techNow)
	if err != nil || len(cands) != 1 || hits.Load() != 2 {
		t.Fatalf("cands=%d err=%v hits=%d", len(cands), err, hits.Load())
	}
}

func TestTechNewsGoogleNewsRetriesOnce(t *testing.T) {
	gn := newFlakyServer(t, gnOK, 503)
	c := techConnector("http://127.0.0.1:1", gn.URL)
	cands, err := c.searchGoogleNews(context.Background(), "kubernetes", &types.SearchRequest{Query: "kubernetes"})
	if err != nil || len(cands) != 1 || gn.hits.Load() != 2 {
		t.Fatalf("cands=%d err=%v hits=%d", len(cands), err, gn.hits.Load())
	}
	gn404 := newFlakyServer(t, gnOK, 404)
	c = techConnector("http://127.0.0.1:1", gn404.URL)
	if _, err := c.searchGoogleNews(context.Background(), "kubernetes", &types.SearchRequest{Query: "kubernetes"}); err == nil || gn404.hits.Load() != 1 {
		t.Fatalf("404 must not retry: err=%v hits=%d", err, gn404.hits.Load())
	}
}

func TestNewsGoogleNewsRSSRetriesOnce(t *testing.T) {
	gn := newFlakyServer(t, gnOK, 429)
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(searxngJSON(1, "bing news", "")))
	}))
	defer searx.Close()
	c := NewNewsConnector(searx.URL, cache.NewService(300))
	c.SetGoogleNewsRSS(gn.URL)
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "kubernetes", MaxResults: 5})
	if err != nil || gn.hits.Load() != 2 || len(resp.Warnings) != 0 {
		t.Fatalf("err=%v hits=%d warnings=%v", err, gn.hits.Load(), resp.Warnings)
	}
}

func TestCuratedFeedsAreNotRetried(t *testing.T) {
	feed := newFlakyServer(t, gnOK, 503)
	_, err := fetchFeed(context.Background(), &http.Client{Timeout: time.Second}, feed.URL)
	if err == nil || feed.hits.Load() != 1 {
		t.Fatalf("curated feed fetch must not retry: err=%v hits=%d", err, feed.hits.Load())
	}
}

func TestRetryWaitsTheBackoffAndHonoursContext(t *testing.T) {
	old := retryBackoff
	retryBackoff = 150 * time.Millisecond
	defer func() { retryBackoff = old }()

	hn := newFlakyServer(t, hnOK, 503)
	c := techConnector(hn.URL, "http://127.0.0.1:1")
	start := time.Now()
	if _, err := c.searchHN(context.Background(), "kubernetes", "", techNow); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d < 140*time.Millisecond {
		t.Errorf("retry came after %v, want at least the backoff", d)
	}

	hn2 := newFlakyServer(t, hnOK, 503, 503)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start = time.Now()
	_, err := techConnector(hn2.URL, "http://127.0.0.1:1").searchHN(ctx, "kubernetes", "", techNow)
	if err == nil {
		t.Fatal("a cancelled context must fail the search")
	}
	if d := time.Since(start); d > 120*time.Millisecond {
		t.Errorf("cancelled context waited %v for the backoff", d)
	}
	if hn2.hits.Load() != 1 {
		t.Errorf("no second request after the context ended, hits=%d", hn2.hits.Load())
	}
}
