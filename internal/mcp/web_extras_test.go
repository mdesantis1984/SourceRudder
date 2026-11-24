package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/connectors"
)

// extrasFake serves page 1 with the given extras JSON fragment (may be empty)
// and eight results.
func extrasFake(t *testing.T, extrasJSON string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		items := make([]string, 0, 8)
		for i, u := range numbered("x", 8) {
			items = append(items, fmt.Sprintf(`{"title":"t%d","url":%q,"content":"c%d","engine":"bing"}`, i, u, i))
		}
		body := `{"results":[` + strings.Join(items, ",") + `],"unresponsive_engines":[]`
		if extrasJSON != "" && (r.URL.Query().Get("pageno") == "" || r.URL.Query().Get("pageno") == "1") {
			body += "," + extrasJSON
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body + "}"))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const mcpExtras = `"answers":["42\nis the answer",{"answer":"obj answer"},null],
"infoboxes":[{"infobox":"Albert Einstein","content":"Physicist","id":"https://en.wikipedia.org/wiki/Albert_Einstein"},"junk"],
"suggestions":["go generics","go modules"],"corrections":["golang"]`

func newExtrasServer(t *testing.T, srv *httptest.Server) *Server {
	t.Helper()
	return newResultSetServer(t, connectors.NewWebConnector(srv.URL, cache.NewService(300)))
}

func assertExtrasStructured(t *testing.T, structured map[string]interface{}) {
	t.Helper()
	want := map[string]interface{}{
		"answers":     []interface{}{"42 is the answer", "obj answer"},
		"infoboxes":   []interface{}{map[string]interface{}{"title": "Albert Einstein", "content": "Physicist", "url": "https://en.wikipedia.org/wiki/Albert_Einstein"}},
		"suggestions": []interface{}{"go generics", "go modules"},
		"corrections": []interface{}{"golang"},
	}
	for k, v := range want {
		if !reflect.DeepEqual(structured[k], v) {
			t.Errorf("structuredContent[%q] = %#v, want %#v", k, structured[k], v)
		}
	}
}

func TestSearchWebExposesSearxngExtrasThroughTheToolResult(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		srv, _ := extrasFake(t, mcpExtras)
		s := newExtrasServer(t, srv)

		env := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "extras tool " + t.Name(), "maxResults": 3})
		structured := decodeObject(t, env.StructuredContent)
		assertExtrasStructured(t, structured)
		cursor, _ := structured["nextCursor"].(string)
		if cursor == "" {
			t.Fatal("fixture must page")
		}
		// The text mirrors structuredContent.
		if !reflect.DeepEqual(decodeObject(t, []byte(env.text(t))), structured) {
			t.Errorf("json text differs from structuredContent")
		}

		// Decision: the extras belong to the search, so every page of the stored
		// set (same as warnings) carries them.
		page2 := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "extras tool " + t.Name(), "cursor": cursor})
		assertExtrasStructured(t, decodeObject(t, page2.StructuredContent))

		compact := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "extras tool " + t.Name(), "maxResults": 3, "format": "compact"})
		text := compact.text(t)
		for _, want := range []string{
			"answer: 42 is the answer",
			"answer: obj answer",
			"infobox: Albert Einstein | Physicist | https://en.wikipedia.org/wiki/Albert_Einstein",
			"suggestions: go generics, go modules",
			"corrections: golang",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("compact text missing %q:\n%s", want, text)
			}
		}
		assertExtrasStructured(t, decodeObject(t, compact.StructuredContent))
	})
}

func TestSearchWebCacheHitKeepsSearxngExtras(t *testing.T) {
	srv, hits := extrasFake(t, mcpExtras)
	s := newExtrasServer(t, srv)
	args := map[string]interface{}{"query": "extras cache hit", "maxResults": 3}
	first := mustEnvelope(t, callPaths["HandleToolsCall"], s, "search_web", args)
	second := mustEnvelope(t, callPaths["HandleToolsCall"], s, "search_web", args)
	if hits.Load() != 1 {
		t.Fatalf("second call must be served from cache, upstream hits=%d", hits.Load())
	}
	st := decodeObject(t, second.StructuredContent)
	if st["cached"] != true {
		t.Fatalf("second call not cached: %v", st["cached"])
	}
	assertExtrasStructured(t, decodeObject(t, first.StructuredContent))
	assertExtrasStructured(t, st)
}

func TestSearchWebHasNoExtrasFieldsWhenSearxngSendsNone(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		srv, _ := extrasFake(t, "")
		s := newExtrasServer(t, srv)
		env := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "no extras " + t.Name(), "maxResults": 3, "format": "compact"})
		structured := decodeObject(t, env.StructuredContent)
		for _, k := range []string{"answers", "infoboxes", "suggestions", "corrections"} {
			if _, ok := structured[k]; ok {
				t.Errorf("%s must be omitted, got %v", k, structured[k])
			}
		}
		for _, label := range []string{"answer:", "infobox:", "suggestions:", "corrections:"} {
			if strings.Contains(env.text(t), label) {
				t.Errorf("compact text must not contain %q:\n%s", label, env.text(t))
			}
		}
	})
}

func TestSearchOutputSchemaDeclaresSearxngExtras(t *testing.T) {
	props := searchOutputSchema()["properties"].(map[string]interface{})
	for _, k := range []string{"answers", "suggestions", "corrections"} {
		p, ok := props[k].(map[string]interface{})
		if !ok || p["type"] != "array" || p["items"].(map[string]interface{})["type"] != "string" {
			t.Errorf("%s must be an array of strings: %v", k, props[k])
		}
	}
	boxes, ok := props["infoboxes"].(map[string]interface{})
	if !ok || boxes["type"] != "array" {
		t.Fatalf("infoboxes must be an array: %v", props["infoboxes"])
	}
	for _, tool := range buildTestServer(t).ListTools() {
		if tool["name"] != "search_web" {
			continue
		}
		raw, _ := json.Marshal(tool["outputSchema"])
		for _, k := range []string{`"answers"`, `"infoboxes"`, `"suggestions"`, `"corrections"`} {
			if !strings.Contains(string(raw), k) {
				t.Errorf("search_web outputSchema lacks %s", k)
			}
		}
	}
}
