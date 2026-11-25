package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// toolsLister returns the tools/list tools through one public path as decoded
// JSON, so the three serialization sites are checked on the wire shape.
type toolsLister func(t *testing.T, s *Server) []map[string]interface{}

var listPaths = map[string]toolsLister{
	"ListTools": func(t *testing.T, s *Server) []map[string]interface{} {
		return roundTripTools(t, s.ListTools())
	},
	"HandleToolsList": func(t *testing.T, s *Server) []map[string]interface{} {
		res, err := s.HandleToolsList(context.Background(), json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("HandleToolsList: %v", err)
		}
		return roundTripTools(t, res.(map[string]interface{})["tools"])
	},
	"HTTP /mcp": func(t *testing.T, s *Server) []map[string]interface{} {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		s.HandleHTTP(rec, req)
		var rpc struct {
			Result struct {
				Tools []map[string]interface{} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rpc); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
		return rpc.Result.Tools
	},
}

func roundTripTools(t *testing.T, v interface{}) []map[string]interface{} {
	t.Helper()
	wire, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var tools []map[string]interface{}
	if err := json.Unmarshal(wire, &tools); err != nil {
		t.Fatal(err)
	}
	return tools
}

func forEachListPath(t *testing.T, fn func(t *testing.T, list toolsLister)) {
	t.Helper()
	for name, list := range listPaths {
		t.Run(name, func(t *testing.T) { fn(t, list) })
	}
}

// openWorldFalse names the tools that never reach beyond the process (the
// synthesis tools work on caller-supplied data, the rest read local state).
var openWorldFalse = map[string]bool{
	"search_local_index": true,
	"summarize_results":  true, "deep_research": true, "compare_sources": true,
	"get_cached": true, "invalidate_cache": true, "get_search_history": true, "get_current_date": true,
}

var synthesisTools = []string{"summarize_results", "deep_research", "compare_sources"}

func TestToolsListEveryToolHasTitleAndAnnotations(t *testing.T) {
	forEachListPath(t, func(t *testing.T, list toolsLister) {
		tools := list(t, buildTestServer(t))
		if len(tools) == 0 {
			t.Fatal("no tools listed")
		}
		for _, tool := range tools {
			name := tool["name"].(string)
			title, _ := tool["title"].(string)
			if title == "" || len(title) > 32 || title == name {
				t.Errorf("%s: want a short human title, got %q", name, title)
			}
			ann, ok := tool["annotations"].(map[string]interface{})
			if !ok {
				t.Errorf("%s: annotations missing", name)
				continue
			}
			want := map[string]interface{}{
				"readOnlyHint":    name != "invalidate_cache",
				"destructiveHint": false,
				"idempotentHint":  true,
				"openWorldHint":   !openWorldFalse[name],
			}
			if !reflect.DeepEqual(ann, want) {
				t.Errorf("%s: annotations=%v want %v", name, ann, want)
			}
		}
	})
}

func TestToolsListOutputSchemaOnlyOnSearchAndSynthesisTools(t *testing.T) {
	forEachListPath(t, func(t *testing.T, list toolsLister) {
		for _, tool := range list(t, buildTestServer(t)) {
			name := tool["name"].(string)
			schema, has := tool["outputSchema"].(map[string]interface{})
			wantSchema := strings.HasPrefix(name, "search_") || listContains(synthesisTools, name)
			if has != wantSchema {
				t.Errorf("%s: outputSchema present=%v want %v", name, has, wantSchema)
				continue
			}
			if has && schema["type"] != "object" {
				t.Errorf("%s: outputSchema must be an object schema (MCP structuredContent is an object), got %v", name, schema["type"])
			}
		}
	})
}

func TestToolsListSearchToolsShareOneOutputSchemaWithTheRequiredFields(t *testing.T) {
	forEachListPath(t, func(t *testing.T, list toolsLister) {
		var first map[string]interface{}
		for _, tool := range list(t, buildTestServer(t)) {
			name := tool["name"].(string)
			if !strings.HasPrefix(name, "search_") {
				continue
			}
			schema, _ := tool["outputSchema"].(map[string]interface{})
			if schema == nil {
				t.Errorf("%s: outputSchema missing", name)
				return
			}
			if first == nil {
				first = schema
			} else if !reflect.DeepEqual(first, schema) {
				t.Errorf("%s: search tools must share one outputSchema", name)
			}
		}
		props := first["properties"].(map[string]interface{})
		for _, f := range []string{"query", "results", "total", "resultSetId", "nextCursor", "partial", "warnings", "errors"} {
			if _, ok := props[f]; !ok {
				t.Errorf("outputSchema lacks property %q", f)
			}
		}
		if got := toStrings(first["required"]); !reflect.DeepEqual(got, []string{"query", "results", "total"}) {
			t.Errorf("required=%v", got)
		}
		items := props["results"].(map[string]interface{})["items"].(map[string]interface{})
		if got := toStrings(items["required"]); !reflect.DeepEqual(got, []string{"title", "url", "source"}) {
			t.Errorf("item required=%v", got)
		}
	})
}

func TestToolsListSizeStaysUnderTheTokenBudget(t *testing.T) {
	wire, err := json.Marshal(map[string]interface{}{"tools": buildTestServer(t).ListTools()})
	if err != nil {
		t.Fatal(err)
	}
	// 37,604 bytes before title/annotations/outputSchema; 61,530 with the full
	// search outputSchema (ac1671c); 54,458 with the slim one (T15-T16); 43,062
	// after trimming the repeated descriptions (T18); 43,118 before the web
	// search extras (answers, infoboxes, suggestions, corrections) joined the
	// shared outputSchema, 46,670 with them (searxng-capabilities T8), 49,086
	// with the 30th tool search_tech_news, whose input and output schemas repeat
	// like every search tool's (searxng-capabilities T14), 50,905 once the
	// result item declares publishedAt, engine and author for codemode
	// (searxng-capabilities T16). Guards are +~3%.
	t.Logf("tools/list bytes: %d", len(wire))
	if schema, _ := json.Marshal(searchOutputSchema()); len(schema) > 890 {
		t.Fatalf("the shared search outputSchema is %d bytes (17 tools repeat it); keep it lean", len(schema))
	}
	if len(wire) > 52400 {
		t.Fatalf("tools/list is %d bytes; keep titles and schema descriptions short", len(wire))
	}
}

// T18 (S9, S13): the outputSchemas carry types and required only; their
// property descriptions were dropped to save bytes on every search tool.
func TestToolsListOutputSchemasCarryNoDescriptions(t *testing.T) {
	for _, tool := range buildTestServer(t).ListTools() {
		schema, ok := tool["outputSchema"]
		if !ok {
			continue
		}
		wire, _ := json.Marshal(schema)
		if strings.Contains(string(wire), `"description"`) {
			t.Errorf("%v: outputSchema must not carry descriptions: %s", tool["name"], wire)
		}
	}
}

// T18: the compact+titles advice for clients that truncate long text lives
// only on `format`, and format/detail keep their closed value sets in enum.
func TestToolsListFormatAdviceAppearsOnlyOnFormat(t *testing.T) {
	checked := 0
	for name, tool := range listedTools(t) {
		props, _ := tool["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
		if _, ok := props["format"]; !ok {
			continue
		}
		checked++
		for prop, p := range props {
			pm, _ := p.(map[string]interface{})
			d, _ := pm["description"].(string)
			if strings.Contains(d, "detail=titles") != (prop == "format") {
				t.Errorf("%s.%s: the detail=titles advice must appear only on format; got %q", name, prop, d)
			}
		}
		for prop, want := range map[string]int{"format": 2, "detail": 3} {
			pm, _ := props[prop].(map[string]interface{})
			if enum, _ := pm["enum"].([]interface{}); len(enum) != want {
				t.Errorf("%s.%s must keep its enum (%d values), got %v", name, prop, want, pm["enum"])
			}
		}
	}
	if checked != 17 {
		t.Fatalf("expected 17 search tools with format, got %d", checked)
	}
}

func listContains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func toStrings(v interface{}) []string {
	var out []string
	switch xs := v.(type) {
	case []interface{}:
		for _, x := range xs {
			out = append(out, x.(string))
		}
	case []string:
		out = xs
	}
	return out
}

// mustToolError runs a tool call that must fail as a tool execution error
// (MCP 2025-11-25: isError result, not a JSON-RPC error) and returns its
// text.
func mustToolError(t *testing.T, call toolCaller, s *Server, tool string, args map[string]interface{}) string {
	t.Helper()
	env, err := call(t, s, tool, args)
	if err != nil {
		t.Fatalf("%s %v: must be an isError result, got a JSON-RPC error: %v", tool, args, err)
	}
	if !env.IsError {
		t.Fatalf("%s %v: isError must be true, got %+v", tool, args, env)
	}
	if len(env.StructuredContent) != 0 {
		t.Fatalf("%s %v: an error result must not carry structuredContent: %s", tool, args, env.StructuredContent)
	}
	return env.text(t)
}

func TestToolErrorsAreIsErrorResultsWithActionableText(t *testing.T) {
	cases := []struct {
		name string
		tool string
		args map[string]interface{}
		want string
	}{
		{"unknown format", "search_web", map[string]interface{}{"query": "q", "format": "xml"}, `format "xml" is not allowed`},
		{"bad cursor", "search_web", map[string]interface{}{"query": "q", "cursor": "%%%"}, "invalid_cursor: "},
		{"wrong argument type", "search_web", map[string]interface{}{"query": 5}, "invalid args: "},
		{"synthesis bad results", "deep_research", map[string]interface{}{"query": "q", "results": "nope"}, "invalid args: "},
		{"cache key required", "get_cached", map[string]interface{}{}, "key is required"},
		{"invalidate key required", "invalidate_cache", map[string]interface{}{}, "key is required"},
		{"history limit required", "get_search_history", map[string]interface{}{}, "limit is required and must be positive"},
		{"fetch needs url", "fetch_url", map[string]interface{}{}, ""},
	}
	forEachPath(t, func(t *testing.T, call toolCaller) {
		for _, tc := range cases {
			s := newResultSetServer(t, &fakeResultsConnector{name: "web", n: 2})
			msg := mustToolError(t, call, s, tc.tool, tc.args)
			if !strings.Contains(msg, tc.want) {
				t.Errorf("%s: text %q must contain %q", tc.name, msg, tc.want)
			}
		}
	})
}

func TestHandlerFailureIsAnIsErrorResultOnBothPaths(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		s := newResultSetServer(t)
		s.toolsRegistry = append(s.toolsRegistry, Tool{Name: "test_fail", Handler: func(context.Context, json.RawMessage) (interface{}, error) {
			return nil, context.DeadlineExceeded
		}})
		if msg := mustToolError(t, call, s, "test_fail", map[string]interface{}{}); msg != "context deadline exceeded" {
			t.Fatalf("text=%q", msg)
		}
		s.toolsRegistry = append(s.toolsRegistry, Tool{Name: "test_fail_empty", Handler: func(context.Context, json.RawMessage) (interface{}, error) {
			return nil, errEmpty{}
		}})
		if msg := mustToolError(t, call, s, "test_fail_empty", map[string]interface{}{}); msg == "" {
			t.Fatal("an empty error message must still produce actionable text")
		}
	})
}

type errEmpty struct{}

func (errEmpty) Error() string { return "" }

func TestUnknownToolStaysAProtocolError(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		_, err := call(t, buildTestServer(t), "no_such_tool", map[string]interface{}{})
		if err == nil || !strings.Contains(err.Error(), "tool not found: no_such_tool") {
			t.Fatalf("unknown tool must stay a JSON-RPC error, got %v", err)
		}
	})
}

func TestMalformedToolsCallParamsStayProtocolErrors(t *testing.T) {
	s := buildTestServer(t)
	if _, err := s.HandleToolsCall(context.Background(), json.RawMessage(`{"name":5}`)); err == nil {
		t.Fatal("HandleToolsCall: malformed params must be an error")
	}
	for _, params := range []string{`{"name":5}`, `{"arguments":{}}`} {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":` + params + `}`)
		rec := httptest.NewRecorder()
		s.HandleHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body)))
		var rpc struct {
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rpc); err != nil || rpc.Error == nil || rpc.Error.Code != -32602 {
			t.Fatalf("params %s: want JSON-RPC -32602, got %s", params, rec.Body.String())
		}
	}
}

func TestSuccessfulResultsAreNotMarkedAsErrors(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		env := mustEnvelope(t, call, newResultSetServer(t, &fakeResultsConnector{name: "web", n: 2}), "search_web", map[string]interface{}{"query": "q"})
		if env.IsError {
			t.Fatal("a successful call must not set isError")
		}
	})
}
