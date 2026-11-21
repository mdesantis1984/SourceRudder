package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/connectors"
	"github.com/mdesantis1984/SourceRudder/internal/resultset"
)

// lazySearxng serves pages of URLs and records the requests it receives.
type lazySearxng struct {
	*httptest.Server
	mu    sync.Mutex
	reqs  []string // pageno per request ("" for page 1)
	forms []map[string]string
	pages func(pageno string) []string
	fail  map[string]bool
}

func newLazySearxng(t *testing.T, pages func(pageno string) []string) *lazySearxng {
	t.Helper()
	l := &lazySearxng{pages: pages, fail: map[string]bool{}}
	l.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		pn := q.Get("pageno")
		page := pn
		if page == "" {
			page = "1"
		}
		l.mu.Lock()
		l.reqs = append(l.reqs, pn)
		l.forms = append(l.forms, map[string]string{
			"q": q.Get("q"), "language": q.Get("language"), "time_range": q.Get("time_range"), "safesearch": q.Get("safesearch"),
		})
		fail := l.fail[page]
		urls := l.pages(page)
		l.mu.Unlock()
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		items := make([]string, 0, len(urls))
		for i, u := range urls {
			items = append(items, fmt.Sprintf(`{"title":"t%d","url":%q,"content":"c","engine":"bing"}`, i, u))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[` + strings.Join(items, ",") + `],"unresponsive_engines":[]}`))
	}))
	t.Cleanup(l.Close)
	return l
}

func (l *lazySearxng) requests() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.reqs...)
}

func (l *lazySearxng) lastForm() map[string]string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.forms[len(l.forms)-1]
}

// thirtyPerPage returns 30 unique URLs per page, except that the first URL of
// every page after the first repeats the last URL of the previous one.
func thirtyPerPage(page string) []string {
	var n int
	fmt.Sscanf(page, "%d", &n)
	out := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		idx := (n-1)*29 + i // consecutive pages overlap by one URL
		out = append(out, fmt.Sprintf("https://example.test/r/%d", idx))
	}
	return out
}

func newLazyWebServer(t *testing.T, sx *lazySearxng) *Server {
	t.Helper()
	return newResultSetServer(t, connectors.NewWebConnector(sx.URL, cache.NewService(300)))
}

func TestWebCursorPastEndFetchesOneExtraPagePerExhaustedPage(t *testing.T) {
	sx := newLazySearxng(t, thirtyPerPage)
	s := newLazyWebServer(t, sx)

	page := mustPaged(t, s, "search_web", map[string]interface{}{"query": "lazy walk"})
	if got := sx.requests(); len(got) != 1 {
		t.Fatalf("search time must keep today's rule (30 unique >= 20: only page 1), got %v", got)
	}
	if len(page.Results) != 20 || page.NextCursor == "" || *page.Total != 30 {
		t.Fatalf("first page: results=%d total=%v cursor=%q", len(page.Results), page.Total, page.NextCursor)
	}

	seen := map[string]bool{}
	for _, r := range page.Results {
		seen[r.URL] = true
	}
	calls := 0
	for page.NextCursor != "" {
		calls++
		if calls > 12 {
			t.Fatalf("paging did not terminate; requests=%v", sx.requests())
		}
		before := len(sx.requests())
		page = mustPaged(t, s, "search_web", map[string]interface{}{"query": "lazy walk", "cursor": page.NextCursor})
		if added := len(sx.requests()) - before; added > 1 {
			t.Fatalf("one paging call fetched %d upstream pages", added)
		}
		for _, r := range page.Results {
			if seen[r.URL] {
				t.Fatalf("duplicate URL served across pages: %s", r.URL)
			}
			seen[r.URL] = true
		}
	}
	want := []string{"", "2", "3", "4", "5"}
	if got := sx.requests(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("upstream requests = %v, want %v (bounded to 5 pages)", got, want)
	}
	// 5 pages x 30 URLs with 4 overlaps.
	if len(seen) != 5*30-4 {
		t.Fatalf("unique URLs served = %d, want %d", len(seen), 5*30-4)
	}
	if page.NextCursor != "" {
		t.Fatalf("nextCursor must be omitted once the 5-page bound is reached")
	}
}

func TestWebLazyPagingContinuesAfterSearchTimePageTwo(t *testing.T) {
	sx := newLazySearxng(t, func(page string) []string {
		switch page {
		case "1":
			return thirtyPerPage("1")[:12]
		case "2":
			return thirtyPerPage("2")[:12]
		default:
			return thirtyPerPage(page)
		}
	})
	s := newLazyWebServer(t, sx)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "after p2", "maxResults": 100})
	if got := strings.Join(sx.requests(), ","); got != ",2" {
		t.Fatalf("search-time requests = %q, want page 1 then 2", got)
	}
	if first.NextCursor == "" {
		t.Fatalf("web set may have more upstream pages: nextCursor expected")
	}
	next := mustPaged(t, s, "search_web", map[string]interface{}{"query": "after p2", "cursor": first.NextCursor})
	if got := strings.Join(sx.requests(), ","); got != ",2,3" {
		t.Fatalf("first lazy request must be pageno=3, got %q", got)
	}
	if len(next.Results) == 0 {
		t.Fatalf("lazy page returned no results")
	}
}

func TestWebLazyPagingStopsWhenPageAddsNoNewURLs(t *testing.T) {
	sx := newLazySearxng(t, func(string) []string { return thirtyPerPage("1") })
	s := newLazyWebServer(t, sx)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "dup only", "maxResults": 30})
	if first.NextCursor == "" {
		t.Fatalf("expected a cursor while upstream may have more")
	}
	next := mustPaged(t, s, "search_web", map[string]interface{}{"query": "dup only", "cursor": first.NextCursor})
	if len(next.Results) != 0 || next.NextCursor != "" {
		t.Fatalf("a page with no new URLs ends paging: results=%d cursor=%q", len(next.Results), next.NextCursor)
	}
	// Replaying the old cursor must not query upstream again.
	again := mustPaged(t, s, "search_web", map[string]interface{}{"query": "dup only", "cursor": first.NextCursor})
	if len(again.Results) != 0 || again.NextCursor != "" {
		t.Fatalf("replay: results=%d cursor=%q", len(again.Results), again.NextCursor)
	}
	if got := strings.Join(sx.requests(), ","); got != ",2" {
		t.Fatalf("requests = %q, want exactly page 1 and one lazy page", got)
	}
}

func TestWebLazyPageForwardsOriginalRequestContext(t *testing.T) {
	sx := newLazySearxng(t, thirtyPerPage)
	s := newLazyWebServer(t, sx)
	first := mustPaged(t, s, "search_web", map[string]interface{}{
		"query": "  ctx query ", "maxResults": 30, "language": "es", "timeRange": "week", "safeSearch": true,
	})
	// The caller's query on the paging call differs; the stored context wins.
	mustPaged(t, s, "search_web", map[string]interface{}{"query": "other", "cursor": first.NextCursor})
	want := map[string]string{"q": "ctx query", "language": "es", "time_range": "week", "safesearch": "1"}
	got := sx.lastForm()
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("lazy request %s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if sx.requests()[1] != "2" {
		t.Fatalf("lazy request must use pageno=2, got %v", sx.requests())
	}
}

func TestWebLazyPageFailureKeepsStoredSetAndRetry(t *testing.T) {
	sx := newLazySearxng(t, thirtyPerPage)
	sx.fail["2"] = true
	s := newLazyWebServer(t, sx)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "fail lazy", "maxResults": 30})
	res := mustPaged(t, s, "search_web", map[string]interface{}{"query": "fail lazy", "cursor": first.NextCursor})
	if len(res.Results) != 0 || len(res.Warnings) == 0 || res.NextCursor == "" {
		t.Fatalf("failed lazy page: want empty page, a warning and a retry cursor, got results=%d warnings=%v cursor=%q", len(res.Results), res.Warnings, res.NextCursor)
	}
	sx.mu.Lock()
	sx.fail["2"] = false
	sx.mu.Unlock()
	ok := mustPaged(t, s, "search_web", map[string]interface{}{"query": "fail lazy", "cursor": res.NextCursor})
	if len(ok.Results) == 0 {
		t.Fatalf("retry should now fetch page 2")
	}
}

func TestWebLazyPagingRespectsResultBudget(t *testing.T) {
	sx := newLazySearxng(t, thirtyPerPage)
	s := newLazyWebServer(t, sx)
	s.SetResultSetStore(resultset.New(resultset.Config{MaxResults: 40}))
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "budget", "maxResults": 30})
	next := mustPaged(t, s, "search_web", map[string]interface{}{"query": "budget", "cursor": first.NextCursor, "maxResults": 100})
	if len(next.Results) != 10 {
		t.Fatalf("only 10 results fit the 40-result budget, got %d", len(next.Results))
	}
	if next.NextCursor != "" || *next.Total != 40 {
		t.Fatalf("budget reached: cursor=%q total=%v", next.NextCursor, next.Total)
	}
	if len(next.Warnings) == 0 {
		t.Fatalf("a budget stop must be reported as a warning")
	}
}

func TestWebConcurrentLazyPagingFetchesEachPageOnce(t *testing.T) {
	sx := newLazySearxng(t, thirtyPerPage)
	s := newLazyWebServer(t, sx)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "race lazy", "maxResults": 30})
	var wg sync.WaitGroup
	var failures int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := callPaged(t, s, "search_web", map[string]interface{}{"query": "race lazy", "cursor": first.NextCursor, "maxResults": 100})
			if err != nil || len(res.Results) != 29 {
				atomic.AddInt32(&failures, 1)
			}
		}()
	}
	wg.Wait()
	if failures != 0 {
		t.Fatalf("%d concurrent callers did not get the same 29 new results", failures)
	}
	if got := strings.Join(sx.requests(), ","); got != ",2" {
		t.Fatalf("page 2 must be fetched exactly once, requests = %q", got)
	}
}

func TestNonWebCursorAtEndOfStoredSetHasNoNextCursor(t *testing.T) {
	fake := &fakeResultsConnector{name: "npm", n: 25}
	s := newResultSetServer(t, fake)
	first := mustPaged(t, s, "search_npm", map[string]interface{}{"query": "no lazy"})
	if first.NextCursor == "" {
		t.Fatalf("25 stored, 10 shown: cursor expected")
	}
	last := mustPaged(t, s, "search_npm", map[string]interface{}{"query": "no lazy", "cursor": first.NextCursor, "maxResults": 100})
	if len(last.Results) != 15 || last.NextCursor != "" {
		t.Fatalf("end of stored set: results=%d cursor=%q", len(last.Results), last.NextCursor)
	}
	if fake.callCount() != 1 {
		t.Fatalf("non-web paging must never search again, calls=%d", fake.callCount())
	}
}
