package mcp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// pagedWebConnector is a web connector that serves a fixed first page and a
// fixed second page through the webPager capability, recording what it saw.
type pagedWebConnector struct {
	first, second []types.SearchResultItem
	mu            sync.Mutex
	searchReqs    []types.SearchRequest
	pageReqs      []types.SearchRequest
}

func (p *pagedWebConnector) Name() string { return "web" }

func (p *pagedWebConnector) Search(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	p.mu.Lock()
	p.searchReqs = append(p.searchReqs, *req)
	p.mu.Unlock()
	return &types.SearchResponse{Query: req.Query, Results: append([]types.SearchResultItem{}, p.first...), SourcesUsed: []string{"web"}, SearxngPages: 1}, nil
}

func (p *pagedWebConnector) NextPage(_ context.Context, _ string, _ int, req *types.SearchRequest, _ map[string]struct{}, _ float64) ([]types.SearchResultItem, error) {
	p.mu.Lock()
	p.pageReqs = append(p.pageReqs, *req)
	p.mu.Unlock()
	return append([]types.SearchResultItem{}, p.second...), nil
}

func hostItem(u string) types.SearchResultItem {
	return types.SearchResultItem{Title: u, URL: u, Source: "web"}
}

func hasWarning(ws []string, want string) bool {
	for _, w := range ws {
		if w == want {
			return true
		}
	}
	return false
}

func urlsOf(r *pagedResponse) []string {
	out := make([]string, len(r.Results))
	for i, it := range r.Results {
		out[i] = it.URL
	}
	return out
}

const (
	warnNoHost1 = "removed 1 results without a valid URL host (cloaked engine links, searxng#6967)"
	warnAdult1  = "removed 1 results from adult domains (safe search)"
)

func TestFirstPageDropsHostlessAndAdultResults(t *testing.T) {
	conn := &pagedWebConnector{first: []types.SearchResultItem{
		hostItem("https://en.wikipedia.org/wiki/Paris"),
		hostItem("http:///go/v1/AaKPaj"),
		hostItem("https://forum.xnxx.com/t/1"),
		hostItem("https://essex.ac.uk/a"),
		hostItem("https://sussex.ac.uk/b"),
	}}
	s := newResultSetServer(t, conn)
	out := mustPaged(t, s, "search_web", map[string]interface{}{"query": "paris"})
	got := strings.Join(urlsOf(out), ",")
	if got != "https://en.wikipedia.org/wiki/Paris,https://essex.ac.uk/a,https://sussex.ac.uk/b" {
		t.Fatalf("results=%s", got)
	}
	if out.Total == nil || *out.Total != 3 {
		t.Fatalf("total=%v want 3", out.Total)
	}
	if !hasWarning(out.Warnings, warnNoHost1) || !hasWarning(out.Warnings, warnAdult1) {
		t.Fatalf("warnings=%v", out.Warnings)
	}
}

func TestExplicitSafeSearchFalseKeepsAdultButStillDropsHostless(t *testing.T) {
	conn := &pagedWebConnector{first: []types.SearchResultItem{
		hostItem("https://forum.xnxx.com/t/1"),
		hostItem("http:///go/v1/AaKPaj"),
		hostItem("https://go.dev/"),
	}}
	s := newResultSetServer(t, conn)
	out := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "safeSearch": false})
	if got := strings.Join(urlsOf(out), ","); got != "https://forum.xnxx.com/t/1,https://go.dev/" {
		t.Fatalf("results=%s", got)
	}
	if hasWarning(out.Warnings, warnAdult1) || !hasWarning(out.Warnings, warnNoHost1) {
		t.Fatalf("warnings=%v", out.Warnings)
	}
}

func TestSafeSearchDefaultsToTrueAndExplicitValueWins(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want bool
	}{
		{"omitted", map[string]interface{}{"query": "q"}, true},
		{"explicit true", map[string]interface{}{"query": "q", "safeSearch": true}, true},
		{"explicit false", map[string]interface{}{"query": "q", "safeSearch": false}, false},
	}
	for _, tc := range cases {
		conn := &pagedWebConnector{first: []types.SearchResultItem{hostItem("https://go.dev/")}}
		s := newResultSetServer(t, conn)
		mustPaged(t, s, "search_web", tc.args)
		if len(conn.searchReqs) != 1 || conn.searchReqs[0].SafeSearch != tc.want {
			t.Errorf("%s: connector saw %+v, want SafeSearch=%v", tc.name, conn.searchReqs, tc.want)
		}
	}
}

func TestLazyPageTwoIsFilteredAndKeepsEffectiveSafeSearch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		args      map[string]interface{}
		wantSafe  bool
		wantAdult bool
	}{
		{"default on", map[string]interface{}{"query": "golang context", "maxResults": 5}, true, false},
		{"explicit off", map[string]interface{}{"query": "golang context", "maxResults": 5, "safeSearch": false}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := &pagedWebConnector{
				first:  []types.SearchResultItem{hostItem("https://go.dev/")},
				second: []types.SearchResultItem{hostItem("https://forum.xnxx.com/t/1"), hostItem("http:///go/v1/x"), hostItem("https://pkg.go.dev/context")},
			}
			s := newResultSetServer(t, conn)
			first := mustPaged(t, s, "search_web", tc.args)
			if first.NextCursor == "" {
				t.Fatal("web set must be extendable")
			}
			page2 := mustPaged(t, s, "search_web", map[string]interface{}{"cursor": first.NextCursor})
			got := strings.Join(urlsOf(page2), ",")
			want := "https://pkg.go.dev/context"
			if tc.wantAdult {
				want = "https://forum.xnxx.com/t/1,https://pkg.go.dev/context"
			}
			if got != want {
				t.Fatalf("page 2 results=%s want %s", got, want)
			}
			if !hasWarning(page2.Warnings, warnNoHost1) {
				t.Errorf("page 2 warnings=%v", page2.Warnings)
			}
			if hasWarning(page2.Warnings, warnAdult1) != !tc.wantAdult {
				t.Errorf("adult warning mismatch: %v", page2.Warnings)
			}
			if len(conn.pageReqs) != 1 || conn.pageReqs[0].SafeSearch != tc.wantSafe {
				t.Errorf("NextPage saw %+v, want SafeSearch=%v", conn.pageReqs, tc.wantSafe)
			}
		})
	}
}

func TestLocalIndexKeepsItsNonHTTPURLs(t *testing.T) {
	conn := &localURLConnector{}
	s := newResultSetServer(t, conn)
	out := mustPaged(t, s, "search_local_index", map[string]interface{}{"query": "q"})
	if len(out.Results) != 1 || out.Results[0].URL != "local-index://document/a" {
		t.Fatalf("local-index results must stay: %+v", out.Results)
	}
}

type localURLConnector struct{}

func (localURLConnector) Name() string { return "local_index" }
func (localURLConnector) Search(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	return &types.SearchResponse{Query: req.Query, Results: []types.SearchResultItem{{Title: "a", URL: "local-index://document/a", Source: "local_index"}}}, nil
}

func TestQueryOrCursorIsRequired(t *testing.T) {
	const want = "invalid_arguments: provide query or cursor"
	for tool, conn := range searchToolConnectors {
		fake := &fakeResultsConnector{name: conn, n: 3}
		s := newResultSetServer(t, fake)
		for _, args := range []map[string]interface{}{{}, {"query": ""}, {"query": "   "}, {"maxResults": 5}} {
			forEachPath(t, func(t *testing.T, call toolCaller) {
				if msg := mustToolError(t, call, s, tool, args); msg != want {
					t.Errorf("%s %v: message=%q want %q", tool, args, msg, want)
				}
			})
		}
		if fake.callCount() != 0 {
			t.Errorf("%s: a blank call must not search (calls=%d)", tool, fake.callCount())
		}
	}
}

func TestCursorOnlyCallWorksOnEverySearchTool(t *testing.T) {
	for tool, conn := range searchToolConnectors {
		fake := &fakeResultsConnector{name: conn, n: 5}
		s := newResultSetServer(t, fake)
		first := mustPaged(t, s, tool, map[string]interface{}{"query": "q", "maxResults": 2})
		next := mustPaged(t, s, tool, map[string]interface{}{"cursor": first.NextCursor})
		if len(next.Results) != 2 {
			t.Errorf("%s: cursor-only call returned %d results, want 2", tool, len(next.Results))
		}
	}
}

func TestSearchSchemaHasNoRequiredQueryAndDocumentsDefaults(t *testing.T) {
	for _, tool := range newResultSetServer(t).Tools() {
		if !strings.HasPrefix(tool.Name, "search_") {
			continue
		}
		if req, present := tool.InputSchema["required"]; present {
			t.Errorf("%s must not declare required, got %v", tool.Name, req)
		}
		props := tool.InputSchema["properties"].(map[string]interface{})
		safe := props["safeSearch"].(map[string]interface{})["description"].(string)
		if !strings.Contains(safe, "default true") {
			t.Errorf("%s safeSearch description must say default true: %q", tool.Name, safe)
		}
	}
	// the synthesis tools keep their required query.
	for _, tool := range newResultSetServer(t).Tools() {
		if tool.Name == "deep_research" {
			if r, _ := tool.InputSchema["required"].([]string); len(r) != 1 || r[0] != "query" {
				t.Errorf("deep_research required=%v", tool.InputSchema["required"])
			}
		}
	}
}

func TestCursorPageKeepsTheFirstPageSize(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 40}
	s := newResultSetServer(t, fake)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 5})
	if len(first.Results) != 5 {
		t.Fatalf("page 1 = %d", len(first.Results))
	}
	second := mustPaged(t, s, "search_web", map[string]interface{}{"cursor": first.NextCursor})
	if len(second.Results) != 5 || second.Results[0].Title != "web result 5" {
		t.Fatalf("cursor page without maxResults must keep 5: got %d (%v)", len(second.Results), second.Results)
	}
	third := mustPaged(t, s, "search_web", map[string]interface{}{"cursor": second.NextCursor, "maxResults": 7})
	if len(third.Results) != 7 {
		t.Fatalf("explicit maxResults must win: got %d", len(third.Results))
	}
	fourth := mustPaged(t, s, "search_web", map[string]interface{}{"cursor": third.NextCursor})
	if len(fourth.Results) != 5 {
		t.Fatalf("an explicit maxResults must not change the stored size: got %d", len(fourth.Results))
	}
}

func TestFirstPageWithoutMaxResultsKeepsToolDefaults(t *testing.T) {
	for tool, want := range map[string]int{"search_web": 20, "search_news": 10} {
		conn := searchToolConnectors[tool]
		s := newResultSetServer(t, &fakeResultsConnector{name: conn, n: 60})
		first := mustPaged(t, s, tool, map[string]interface{}{"query": "q"})
		next := mustPaged(t, s, tool, map[string]interface{}{"cursor": first.NextCursor})
		if len(first.Results) != want || len(next.Results) != want {
			t.Errorf("%s: first=%d next=%d want %d", tool, len(first.Results), len(next.Results), want)
		}
	}
}

func TestCursorPageSizeIsCappedAtMaxPageSize(t *testing.T) {
	s := newResultSetServer(t, &fakeResultsConnector{name: "web", n: 300})
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 1000})
	if len(first.Results) > maxPageSize {
		t.Fatalf("first page %d exceeds cap", len(first.Results))
	}
	next := mustPaged(t, s, "search_web", map[string]interface{}{"cursor": first.NextCursor})
	if len(next.Results) > maxPageSize {
		t.Fatal(fmt.Sprintf("cursor page %d exceeds cap", len(next.Results)))
	}
}
