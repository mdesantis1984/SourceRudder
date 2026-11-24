package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// T16 (S8, S13): retired paging arguments warn, a cursor only works with the
// tool that issued it, and the three tools/list sites stay byte-identical.

const wantRetiredWarning = "offset and resultSetId are no longer supported; page with cursor=<nextCursor>"

type warnedResponse struct {
	Results  []json.RawMessage `json:"results"`
	Warnings []string          `json:"warnings"`
}

func countWarning(env envelope, want string) (int, int) {
	var out warnedResponse
	if err := json.Unmarshal(env.StructuredContent, &out); err != nil {
		return -1, 0
	}
	n := 0
	for _, w := range out.Warnings {
		if w == want {
			n++
		}
	}
	return n, len(out.Results)
}

func TestRetiredArgumentsGetOneReplacementWarning(t *testing.T) {
	cases := []struct {
		name string
		args map[string]interface{}
		want int
	}{
		{"offset", map[string]interface{}{"query": "q", "offset": 5}, 1},
		{"resultSetId", map[string]interface{}{"query": "q", "resultSetId": "rs_AAAAAAAAAAAAAAAA"}, 1},
		{"both", map[string]interface{}{"query": "q", "offset": 5, "resultSetId": "rs_AAAAAAAAAAAAAAAA"}, 1},
		{"neither", map[string]interface{}{"query": "q"}, 0},
	}
	forEachPath(t, func(t *testing.T, call toolCaller) {
		for _, tc := range cases {
			fake := &fakeResultsConnector{name: "web", n: 3}
			s := newResultSetServer(t, fake)
			env := mustEnvelope(t, call, s, "search_web", tc.args)
			got, results := countWarning(env, wantRetiredWarning)
			if got != tc.want {
				t.Errorf("%s: warning count=%d want %d (%s)", tc.name, got, tc.want, env.StructuredContent)
			}
			if results != 3 || fake.callCount() != 1 {
				t.Errorf("%s: the call must still run as today: results=%d calls=%d", tc.name, results, fake.callCount())
			}
		}
	})
}

func TestRetiredArgumentWarningCoversEverySearchTool(t *testing.T) {
	for tool, conn := range searchToolConnectors {
		fake := &fakeResultsConnector{name: conn, n: 2}
		s := newResultSetServer(t, fake)
		env := mustEnvelope(t, callPaths["HandleToolsCall"], s, tool, map[string]interface{}{"query": "q", "offset": 1})
		if got, _ := countWarning(env, wantRetiredWarning); got != 1 {
			t.Errorf("%s: warning count=%d want 1 (%s)", tool, got, env.StructuredContent)
		}
	}
}

func TestSynthesisToolsAcceptResultSetIDWithoutTheRetiredWarning(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 3}
	s := newResultSetServer(t, fake)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q"})
	for _, tool := range synthesisTools {
		env := mustEnvelope(t, callPaths["HandleToolsCall"], s, tool, map[string]interface{}{"resultSetId": first.ResultSetID})
		if strings.Contains(string(env.StructuredContent), "no longer supported") {
			t.Errorf("%s must not carry the retired-argument warning: %s", tool, env.StructuredContent)
		}
	}
}

func TestCursorIsBoundToTheToolThatIssuedIt(t *testing.T) {
	web := &fakeResultsConnector{name: "web", n: 25}
	news := &fakeResultsConnector{name: "news", n: 25}
	s := newResultSetServer(t, web, news)
	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 10})
	if first.NextCursor == "" {
		t.Fatal("expected a nextCursor")
	}
	forEachPath(t, func(t *testing.T, call toolCaller) {
		msg := mustToolError(t, call, s, "search_news", map[string]interface{}{"query": "q", "cursor": first.NextCursor})
		if msg != "invalid_cursor: cursor was issued by search_web" {
			t.Fatalf("message=%q", msg)
		}
	})
	if news.callCount() != 0 || web.callCount() != 1 {
		t.Fatalf("a mismatched cursor must not search: news=%d web=%d", news.callCount(), web.callCount())
	}
	// The store was not touched: the issuing tool still pages the same set.
	second := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 10, "cursor": first.NextCursor})
	if len(second.Results) != 10 || second.Results[0].Title != "web result 10" || second.ResultSetID != first.ResultSetID {
		t.Fatalf("issuing tool paging broke: %+v", second)
	}
}

func TestEverySearchToolAcceptsItsOwnCursorOnly(t *testing.T) {
	for tool, conn := range searchToolConnectors {
		fake := &fakeResultsConnector{name: conn, n: 5}
		s := newResultSetServer(t, fake)
		first := mustPaged(t, s, tool, map[string]interface{}{"query": "q", "maxResults": 2})
		if first.NextCursor == "" {
			t.Fatalf("%s: no nextCursor", tool)
		}
		next := mustPaged(t, s, tool, map[string]interface{}{"query": "q", "maxResults": 2, "cursor": first.NextCursor})
		if len(next.Results) != 2 {
			t.Errorf("%s: own cursor must page, got %d results", tool, len(next.Results))
		}
	}
}

func TestVersionOneCursorIsInvalidNowThatCursorsAreToolBound(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 5}
	s := newResultSetServer(t, fake)
	v1 := base64.RawURLEncoding.EncodeToString([]byte("v1:rs_ABCDEFGHIJKLMNOP:2"))
	msg := mustToolError(t, callPaths["HandleToolsCall"], s, "search_web", map[string]interface{}{"query": "q", "cursor": v1})
	if !strings.HasPrefix(msg, "invalid_cursor: ") {
		t.Fatalf("v1 cursor must be invalid_cursor, got %q", msg)
	}
	if fake.callCount() != 0 {
		t.Fatalf("an invalid cursor must not search: calls=%d", fake.callCount())
	}
}

func TestToolsListIsByteIdenticalOnEverySerializationSite(t *testing.T) {
	s := buildTestServer(t)
	listed, err := json.Marshal(s.ListTools())
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.HandleToolsList(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	handled, err := json.Marshal(res.(map[string]interface{})["tools"])
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	rec := httptest.NewRecorder()
	s.HandleHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body)))
	var rpc struct {
		Result struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &rpc); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, rpc.Result.Tools); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(listed, handled) || !bytes.Equal(listed, compacted.Bytes()) {
		t.Fatalf("tools arrays differ: ListTools=%d HandleToolsList=%d HTTP=%d bytes", len(listed), len(handled), compacted.Len())
	}
}

func TestSearchOutputSchemaDeclaresOnlyTheSlimSet(t *testing.T) {
	schema := searchOutputSchema()
	props := schema["properties"].(map[string]interface{})
	want := []string{"query", "results", "resultSetId", "total", "nextCursor", "partial", "warnings", "errors", "answers", "infoboxes", "suggestions", "corrections"}
	if len(props) != len(want) {
		t.Errorf("properties=%d want %d: %v", len(props), len(want), props)
	}
	for _, k := range want {
		if _, ok := props[k]; !ok {
			t.Errorf("missing property %q", k)
		}
	}
	if schema["additionalProperties"] == false {
		t.Error("additionalProperties must stay allowed so structuredContent keeps its other fields")
	}
	item := props["results"].(map[string]interface{})["items"].(map[string]interface{})
	itemProps := item["properties"].(map[string]interface{})
	if len(itemProps) != 4 {
		t.Errorf("item properties=%v want title,url,snippet,source", itemProps)
	}
	for _, k := range []string{"title", "url", "snippet", "source"} {
		if _, ok := itemProps[k]; !ok {
			t.Errorf("item lacks %q", k)
		}
	}
}
