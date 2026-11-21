package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/connectors"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// pagedResponse decodes only the wire fields S8 adds, independently of the
// Go struct, so the tests assert the JSON contract.
type pagedResponse struct {
	Query       string                   `json:"query"`
	Results     []types.SearchResultItem `json:"results"`
	ResultSetID string                   `json:"resultSetId"`
	Total       *int                     `json:"total"`
	Offset      *int                     `json:"offset"`
	HasMore     *bool                    `json:"hasMore"`
	Cached      bool                     `json:"cached"`
	Warnings    []string                 `json:"warnings"`
}

var resultSetIDPattern = regexp.MustCompile(`^rs_[A-Z2-7]{16}$`)

func callPaged(t *testing.T, s *Server, tool string, args map[string]interface{}) (*pagedResponse, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	params, _ := json.Marshal(map[string]interface{}{"name": tool, "arguments": json.RawMessage(raw)})
	res, err := s.HandleToolsCall(context.Background(), params)
	if err != nil {
		return nil, err
	}
	content := res.(map[string]interface{})["content"].([]map[string]interface{})
	var out pagedResponse
	if err := json.Unmarshal([]byte(content[0]["text"].(string)), &out); err != nil {
		t.Fatalf("decode %s response: %v", tool, err)
	}
	return &out, nil
}

func mustPaged(t *testing.T, s *Server, tool string, args map[string]interface{}) *pagedResponse {
	t.Helper()
	out, err := callPaged(t, s, tool, args)
	if err != nil {
		t.Fatalf("%s %v: %v", tool, args, err)
	}
	return out
}

func newResultSetServer(t *testing.T, conns ...types.SearchConnector) *Server {
	t.Helper()
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	for _, c := range conns {
		cm.Register(c)
	}
	return NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10))
}

// fakeResultsConnector returns n results per call and counts its calls.
type fakeResultsConnector struct {
	name     string
	n        int
	strategy string
	calls    int32
}

func (f *fakeResultsConnector) Name() string { return f.name }

func (f *fakeResultsConnector) build(req *types.SearchRequest) *types.SearchResponse {
	atomic.AddInt32(&f.calls, 1)
	items := make([]types.SearchResultItem, f.n)
	for i := range items {
		items[i] = types.SearchResultItem{
			Title:  fmt.Sprintf("%s result %d", f.name, i),
			URL:    fmt.Sprintf("https://example.test/%s/%d", f.name, i),
			Source: f.name,
			Score:  float64(f.n - i),
		}
	}
	return &types.SearchResponse{Query: req.Query, Results: items, SourcesUsed: []string{f.name}, Strategy: f.strategy}
}

func (f *fakeResultsConnector) Search(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	return f.build(req), nil
}
func (f *fakeResultsConnector) SearchPR(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	return f.build(req), nil
}
func (f *fakeResultsConnector) SearchIssue(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	return f.build(req), nil
}
func (f *fakeResultsConnector) callCount() int { return int(atomic.LoadInt32(&f.calls)) }

// searchToolConnectors maps each of the 17 search tools to the connector
// name its handler resolves.
var searchToolConnectors = map[string]string{
	"search_web":           "web",
	"search_github":        "github",
	"search_github_pr":     "github",
	"search_github_issue":  "github",
	"search_stackoverflow": "stackoverflow",
	"search_npm":           "npm",
	"search_nuget":         "nuget",
	"search_pypi":          "pypi",
	"search_docker_hub":    "dockerhub",
	"search_doc_oficial":   "official_docs",
	"search_local_index":   "local_index",
	"search_academic":      "academic",
	"search_reddit":        "reddit",
	"search_youtube":       "youtube",
	"search_images":        "images",
	"search_news":          "news",
}

func TestResultSetSchemaOnEverySearchTool(t *testing.T) {
	s := buildTestServer(t)
	checked := 0
	for _, tool := range s.Tools() {
		if !strings.HasPrefix(tool.Name, "search_") {
			continue
		}
		checked++
		props, _ := tool.InputSchema["properties"].(map[string]interface{})
		offset, ok := props["offset"].(map[string]interface{})
		if !ok || offset["type"] != "integer" || offset["minimum"] != 0 {
			t.Errorf("%s must expose offset as integer with minimum 0, got %v", tool.Name, props["offset"])
		}
		id, ok := props["resultSetId"].(map[string]interface{})
		if !ok || id["type"] != "string" {
			t.Errorf("%s must expose resultSetId as string, got %v", tool.Name, props["resultSetId"])
		}
		required, _ := tool.InputSchema["required"].([]string)
		if len(required) != 1 || required[0] != "query" {
			t.Errorf("%s must keep query as the only required argument, got %v", tool.Name, required)
		}
	}
	if checked != 16 {
		t.Fatalf("expected 16 search_* tools (12 makeSearchHandler + doc_oficial + local_index + github PR/issue; the brief said 17) with the shared schema, got %d", checked)
	}
	if got := len(s.Tools()); got != 29 {
		t.Fatalf("tool count must stay 29, got %d", got)
	}
}

func TestResultSetEverySearchToolStoresFullSetAndReturnsSlice(t *testing.T) {
	for tool, connectorName := range searchToolConnectors {
		t.Run(tool, func(t *testing.T) {
			fake := &fakeResultsConnector{name: connectorName, n: 25, strategy: "official_doc_registry_search"}
			if connectorName != "official_docs" {
				fake.strategy = ""
			}
			s := newResultSetServer(t, fake)

			resp := mustPaged(t, s, tool, map[string]interface{}{"query": "kubernetes", "maxResults": 10})
			if len(resp.Results) != 10 {
				t.Fatalf("results=%d want 10", len(resp.Results))
			}
			if resp.Results[0].Title != connectorName+" result 0" || resp.Results[9].Title != connectorName+" result 9" {
				t.Fatalf("slice must keep connector order: first=%q last=%q", resp.Results[0].Title, resp.Results[9].Title)
			}
			if !resultSetIDPattern.MatchString(resp.ResultSetID) {
				t.Fatalf("resultSetId=%q", resp.ResultSetID)
			}
			if resp.Total == nil || *resp.Total != 25 || resp.Offset == nil || *resp.Offset != 0 || resp.HasMore == nil || !*resp.HasMore {
				t.Fatalf("total/offset/hasMore wrong: %+v", resp)
			}

			page := mustPaged(t, s, tool, map[string]interface{}{"query": "kubernetes", "maxResults": 10, "offset": 20, "resultSetId": resp.ResultSetID})
			if len(page.Results) != 5 || page.Results[0].Title != connectorName+" result 20" {
				t.Fatalf("last page wrong: len=%d", len(page.Results))
			}
			if *page.Total != 25 || *page.Offset != 20 || *page.HasMore || page.ResultSetID != resp.ResultSetID {
				t.Fatalf("last page fields wrong: total=%d offset=%d hasMore=%v id=%s", *page.Total, *page.Offset, *page.HasMore, page.ResultSetID)
			}
			if fake.callCount() != 1 {
				t.Fatalf("paging by resultSetId must not call the connector again; calls=%d", fake.callCount())
			}
		})
	}
}

func TestResultSetPagingByIDIsLocalAndCoversTheWholeSet(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 25}
	s := newResultSetServer(t, fake)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 10})

	var all []string
	for _, r := range first.Results {
		all = append(all, r.Title)
	}
	for offset := 10; offset < 30; offset += 10 {
		page := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 10, "offset": offset, "resultSetId": first.ResultSetID})
		for _, r := range page.Results {
			all = append(all, r.Title)
		}
		if offset == 10 && (*page.HasMore != true || len(page.Results) != 10) {
			t.Fatalf("middle page wrong: len=%d hasMore=%v", len(page.Results), *page.HasMore)
		}
	}
	if len(all) != 25 {
		t.Fatalf("paging must cover all 25 results, got %d", len(all))
	}
	for i, title := range all {
		if title != fmt.Sprintf("web result %d", i) {
			t.Fatalf("order changed at %d: %q", i, title)
		}
	}

	beyond := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "offset": 99, "resultSetId": first.ResultSetID})
	if len(beyond.Results) != 0 || *beyond.Total != 25 || *beyond.HasMore {
		t.Fatalf("offset past the end must be an empty page: %+v", beyond)
	}
	if fake.callCount() != 1 {
		t.Fatalf("connector calls=%d want 1", fake.callCount())
	}
}

func TestResultSetOffsetWithoutIDSlicesTheFreshSearch(t *testing.T) {
	fake := &fakeResultsConnector{name: "npm", n: 25}
	s := newResultSetServer(t, fake)
	resp := mustPaged(t, s, "search_npm", map[string]interface{}{"query": "q", "maxResults": 10, "offset": 10})
	if len(resp.Results) != 10 || resp.Results[0].Title != "npm result 10" || *resp.Offset != 10 || !*resp.HasMore || *resp.Total != 25 {
		t.Fatalf("unexpected offset slice: len=%d offset=%d", len(resp.Results), *resp.Offset)
	}
}

func TestResultSetDifferentQueryAddsWarning(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 12}
	s := newResultSetServer(t, fake)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "original query", "maxResults": 5})

	same := mustPaged(t, s, "search_web", map[string]interface{}{"query": "original query", "maxResults": 5, "offset": 5, "resultSetId": first.ResultSetID})
	for _, w := range same.Warnings {
		if strings.Contains(w, "result set") {
			t.Fatalf("same query must not warn: %q", w)
		}
	}

	other := mustPaged(t, s, "search_web", map[string]interface{}{"query": "something else", "maxResults": 5, "offset": 5, "resultSetId": first.ResultSetID})
	found := false
	for _, w := range other.Warnings {
		if strings.Contains(w, "original query") && strings.Contains(w, first.ResultSetID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("different query must warn naming the stored query; warnings=%v", other.Warnings)
	}
	if other.Query != "original query" || len(other.Results) != 5 {
		t.Fatalf("stored set must still be served: query=%q results=%d", other.Query, len(other.Results))
	}
	if fake.callCount() != 1 {
		t.Fatalf("connector calls=%d want 1", fake.callCount())
	}
}

func TestResultSetUnknownIDIsAToolError(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 3}
	s := newResultSetServer(t, fake)
	_, err := callPaged(t, s, "search_web", map[string]interface{}{"query": "q", "resultSetId": "rs_AAAAAAAAAAAAAAAA"})
	if err == nil {
		t.Fatal("unknown resultSetId must be an error")
	}
	want := "result_set_not_found: rs_AAAAAAAAAAAAAAAA (expired or unknown; run the search again)"
	if err.Error() != want {
		t.Fatalf("error=%q want %q", err.Error(), want)
	}
	if fake.callCount() != 0 {
		t.Fatalf("an unknown id must not trigger a search; calls=%d", fake.callCount())
	}
}

func TestResultSetNegativeOffsetIsRejected(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 3}
	s := newResultSetServer(t, fake)
	_, err := callPaged(t, s, "search_web", map[string]interface{}{"query": "q", "offset": -1})
	if err == nil || !strings.Contains(err.Error(), "offset must be >= 0") {
		t.Fatalf("negative offset must be rejected, got %v", err)
	}
	if fake.callCount() != 0 {
		t.Fatalf("rejected input must not search; calls=%d", fake.callCount())
	}
}

// searxngFake serves numbered pages and counts requests per pageno.
type searxngFake struct {
	*httptest.Server
	mu    sync.Mutex
	pages map[string][]string
	hits  []string
}

func newSearxngFake(t *testing.T, pages map[string][]string) *searxngFake {
	t.Helper()
	f := &searxngFake{pages: pages}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("pageno")
		if page == "" {
			page = "1"
		}
		f.mu.Lock()
		f.hits = append(f.hits, page)
		urls := f.pages[page]
		f.mu.Unlock()
		items := make([]string, 0, len(urls))
		for i, u := range urls {
			items = append(items, fmt.Sprintf(`{"title":"t%d","url":%q,"content":"c%d","engine":"bing","img_src":%q}`, i, u, i, u+".png"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[` + strings.Join(items, ",") + `],"unresponsive_engines":[]}`))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *searxngFake) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hits)
}

func numbered(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("https://example.test/%s/%d", prefix, i)
	}
	return out
}

func newWebServer(t *testing.T, fake *searxngFake) *Server {
	t.Helper()
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	cm.Register(connectors.NewWebConnector(fake.URL, cacheSvc))
	return NewServer(cm, search.NewPlanner(), "stdio", ":8080", fake.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10))
}

func TestResultSetCacheHitReturnsPageableID(t *testing.T) {
	fake := newSearxngFake(t, map[string][]string{"1": numbered("a", 8)})
	s := newWebServer(t, fake)
	args := map[string]interface{}{"query": "cached query", "maxResults": 3}

	first := mustPaged(t, s, "search_web", args)
	second := mustPaged(t, s, "search_web", args)
	if !second.Cached {
		t.Fatal("second call must be a cache hit")
	}
	if fake.requestCount() != 1 {
		t.Fatalf("cache hit must not reach upstream; requests=%d", fake.requestCount())
	}
	if !resultSetIDPattern.MatchString(second.ResultSetID) || second.ResultSetID == first.ResultSetID {
		t.Fatalf("cache hit must get its own pageable id: first=%q second=%q", first.ResultSetID, second.ResultSetID)
	}
	if *second.Total != 8 || len(second.Results) != 3 || !*second.HasMore {
		t.Fatalf("cache hit must expose the full cached set: total=%d len=%d", *second.Total, len(second.Results))
	}
	page := mustPaged(t, s, "search_web", map[string]interface{}{"query": "cached query", "maxResults": 3, "offset": 6, "resultSetId": second.ResultSetID})
	if len(page.Results) != 2 || page.Results[0].URL != "https://example.test/a/6" {
		t.Fatalf("paging a cache-hit id failed: %+v", page.Results)
	}
	if fake.requestCount() != 1 {
		t.Fatalf("paging must stay local; requests=%d", fake.requestCount())
	}
}

// Compatibility pin: a default call (no offset, no resultSetId) returns the
// same results array, item for item, as before S8.
func TestResultSetDefaultCallResultsAreUnchanged(t *testing.T) {
	fake := newSearxngFake(t, map[string][]string{"1": {"https://example.test/one", "https://example.test/two", "https://example.test/three"}})
	s := newWebServer(t, fake)

	resp := mustPaged(t, s, "search_web", map[string]interface{}{"query": "compat"})
	got, _ := json.Marshal(resp.Results)
	want := `[` +
		`{"title":"t0","url":"https://example.test/one","snippet":"c0","source":"searxng","engine":"bing","type":"web","score":20,"citationId":"web:unknown"},` +
		`{"title":"t1","url":"https://example.test/two","snippet":"c1","source":"searxng","engine":"bing","type":"web","score":19,"citationId":"web:unknown"},` +
		`{"title":"t2","url":"https://example.test/three","snippet":"c2","source":"searxng","engine":"bing","type":"web","score":18,"citationId":"web:unknown"}]`
	if string(got) != want {
		t.Fatalf("default results changed:\n got %s\nwant %s", got, want)
	}
	if *resp.Total != 3 || *resp.Offset != 0 || *resp.HasMore {
		t.Fatalf("fields wrong: total=%d offset=%d hasMore=%v", *resp.Total, *resp.Offset, *resp.HasMore)
	}
}

// D4: no extra network requests; the page-2 fill rule is evaluated against
// offset+maxResults.
func TestResultSetWebPageTwoRuleUsesOffsetPlusMaxResults(t *testing.T) {
	pages := map[string][]string{"1": numbered("p1", 5), "2": numbered("p2", 5)}

	// 5 unique results already satisfy maxResults=3: one request, all 5 kept.
	fake := newSearxngFake(t, pages)
	s := newWebServer(t, fake)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 3})
	if fake.requestCount() != 1 || *first.Total != 5 || len(first.Results) != 3 {
		t.Fatalf("maxResults=3: requests=%d total=%d len=%d (want 1/5/3)", fake.requestCount(), *first.Total, len(first.Results))
	}

	// offset=4,maxResults=3 needs 7 results: page 2 is requested exactly once.
	fake2 := newSearxngFake(t, pages)
	s2 := newWebServer(t, fake2)
	resp := mustPaged(t, s2, "search_web", map[string]interface{}{"query": "q", "maxResults": 3, "offset": 4})
	if fake2.requestCount() != 2 {
		t.Fatalf("offset+maxResults=7 > 5 page-1 results: want exactly 2 upstream requests, got %d (%v)", fake2.requestCount(), fake2.hits)
	}
	if *resp.Total != 10 || len(resp.Results) != 3 || resp.Results[0].URL != "https://example.test/p1/4" || resp.Results[1].URL != "https://example.test/p2/0" {
		t.Fatalf("unexpected slice: total=%d %+v", *resp.Total, resp.Results)
	}
}

// Connectors that used to throw away results they had already received now
// keep them; the handler slices.
func TestResultSetConnectorsKeepEveryUniqueResult(t *testing.T) {
	cases := []struct {
		tool string
		urls []string
		make func(url string, c *cache.Service) types.SearchConnector
	}{
		{"search_news", numbered("n", 15), func(u string, c *cache.Service) types.SearchConnector { return connectors.NewNewsConnector(u, c) }},
		{"search_academic", func() []string {
			out := make([]string, 15)
			for i := range out {
				out[i] = fmt.Sprintf("https://arxiv.org/abs/2401.%04d", i)
			}
			return out
		}(), func(u string, c *cache.Service) types.SearchConnector { return connectors.NewAcademicConnector(u, c) }},
		{"search_youtube", func() []string {
			out := make([]string, 15)
			for i := range out {
				out[i] = fmt.Sprintf("https://www.youtube.com/watch?v=abcdefg%04d", i)
			}
			return out
		}(), func(u string, c *cache.Service) types.SearchConnector { return connectors.NewYouTubeConnector(u, c) }},
		{"search_reddit", func() []string {
			out := make([]string, 15)
			for i := range out {
				out[i] = fmt.Sprintf("https://www.reddit.com/r/golang/comments/abc%03d/title/", i)
			}
			return out
		}(), func(u string, c *cache.Service) types.SearchConnector {
			return connectors.NewRedditConnector(connectors.RedditConfig{SearxngURL: u}, c)
		}},
		{"search_images", numbered("img", 15), func(u string, c *cache.Service) types.SearchConnector { return connectors.NewImagesConnector(u, c) }},
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			fake := newSearxngFake(t, map[string][]string{"1": tc.urls})
			cacheSvc := cache.NewService(300)
			cm := search.NewConnectorManager(cacheSvc)
			cm.Register(tc.make(fake.URL, cacheSvc))
			s := NewServer(cm, search.NewPlanner(), "stdio", ":8080", fake.URL, 300, 5000, fetch.NewFetcherService(5000), synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10))

			resp := mustPaged(t, s, tc.tool, map[string]interface{}{"query": "golang", "maxResults": 5})
			if len(resp.Results) != 5 || resp.Total == nil || *resp.Total != 15 || !*resp.HasMore {
				t.Fatalf("want 5 results of 15 stored, got len=%d total=%v", len(resp.Results), resp.Total)
			}
			// Descending score order is unchanged inside the returned window.
			for i := 1; i < len(resp.Results); i++ {
				if resp.Results[i].Score >= resp.Results[i-1].Score {
					t.Fatalf("scores must stay strictly descending: %v then %v", resp.Results[i-1].Score, resp.Results[i].Score)
				}
			}
			rest := mustPaged(t, s, tc.tool, map[string]interface{}{"query": "golang", "maxResults": 20, "offset": 5, "resultSetId": resp.ResultSetID})
			if len(rest.Results) != 10 || *rest.HasMore {
				t.Fatalf("remaining page wrong: len=%d", len(rest.Results))
			}
			if fake.requestCount() != 1 {
				t.Fatalf("upstream requests=%d want 1", fake.requestCount())
			}
		})
	}
}

// The default page window is search_web's 20 even though more unique results
// were fetched; the rest stays pageable.
func TestResultSetWebDefaultWindowIsTwentyAndRestIsPageable(t *testing.T) {
	fake := newSearxngFake(t, map[string][]string{"1": numbered("a", 12), "2": numbered("b", 15)})
	s := newWebServer(t, fake)
	resp := mustPaged(t, s, "search_web", map[string]interface{}{"query": "window"})
	if len(resp.Results) != 20 || *resp.Total != 27 || !*resp.HasMore {
		t.Fatalf("default window wrong: len=%d total=%d hasMore=%v", len(resp.Results), *resp.Total, *resp.HasMore)
	}
	rest := mustPaged(t, s, "search_web", map[string]interface{}{"query": "window", "offset": 20, "resultSetId": resp.ResultSetID})
	if len(rest.Results) != 7 || *rest.HasMore {
		t.Fatalf("rest wrong: len=%d", len(rest.Results))
	}
	if fake.requestCount() != 2 {
		t.Fatalf("upstream requests=%d want 2 (page 1 and page 2 only)", fake.requestCount())
	}
}
