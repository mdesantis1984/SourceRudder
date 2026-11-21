package mcp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// envelope is the tools/call result exactly as a client decodes it.
type envelope struct {
	Content           []envelopeContent `json:"content"`
	StructuredContent json.RawMessage   `json:"structuredContent"`
	IsError           bool              `json:"isError"`
}

type envelopeContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (e envelope) text(t *testing.T) string {
	t.Helper()
	if len(e.Content) != 1 || e.Content[0].Type != "text" {
		t.Fatalf("want exactly one text content item, got %+v", e.Content)
	}
	if e.Content[0].Text == "" {
		t.Fatal("the text block must never be empty")
	}
	return e.Content[0].Text
}

// toolCaller runs tools/call through one public path and returns the decoded
// result, or the error message when the call failed.
type toolCaller func(t *testing.T, s *Server, tool string, args map[string]interface{}) (envelope, error)

// callPaths covers both tools/call implementations: the in-process
// HandleToolsCall and the JSON-RPC POST that the streamable HTTP transport
// serves.
var callPaths = map[string]toolCaller{
	"HandleToolsCall": func(t *testing.T, s *Server, tool string, args map[string]interface{}) (envelope, error) {
		t.Helper()
		raw, _ := json.Marshal(args)
		params, _ := json.Marshal(map[string]interface{}{"name": tool, "arguments": json.RawMessage(raw)})
		res, err := s.HandleToolsCall(context.Background(), params)
		if err != nil {
			return envelope{}, err
		}
		wire, err := json.Marshal(res)
		if err != nil {
			t.Fatalf("marshal result: %v", err)
		}
		var env envelope
		if err := json.Unmarshal(wire, &env); err != nil {
			t.Fatalf("decode result %s: %v", wire, err)
		}
		return env, nil
	},
	"HTTP /mcp": func(t *testing.T, s *Server, tool string, args map[string]interface{}) (envelope, error) {
		t.Helper()
		body, _ := json.Marshal(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]interface{}{"name": tool, "arguments": args},
		})
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		s.HandleHTTP(rec, req)
		var rpc struct {
			Result *envelope `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &rpc); err != nil {
			t.Fatalf("decode rpc %s: %v", rec.Body.String(), err)
		}
		if rpc.Error != nil {
			return envelope{}, fmt.Errorf("%s", rpc.Error.Message)
		}
		if rpc.Result == nil {
			t.Fatalf("rpc response without result or error: %s", rec.Body.String())
		}
		return *rpc.Result, nil
	},
}

func forEachPath(t *testing.T, fn func(t *testing.T, call toolCaller)) {
	t.Helper()
	for name, call := range callPaths {
		t.Run(name, func(t *testing.T) { fn(t, call) })
	}
}

func mustEnvelope(t *testing.T, call toolCaller, s *Server, tool string, args map[string]interface{}) envelope {
	t.Helper()
	env, err := call(t, s, tool, args)
	if err != nil {
		t.Fatalf("%s %v: %v", tool, args, err)
	}
	return env
}

func decodeObject(t *testing.T, raw []byte) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("not a JSON object: %q: %v", raw, err)
	}
	return out
}

func TestEnvelopeDefaultTextIsMinifiedJSONMirroringStructuredContent(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		fake := &fakeResultsConnector{name: "web", n: 25}
		s := newResultSetServer(t, fake)
		env := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "kubernetes", "maxResults": 10})

		text := env.text(t)
		if strings.Contains(text, "\n") || strings.Contains(text, "  ") || strings.Contains(text, `": `) {
			t.Fatalf("default text must be minified JSON, got %.200q", text)
		}
		var compacted bytes.Buffer
		if err := json.Compact(&compacted, []byte(text)); err != nil || compacted.String() != text {
			t.Fatalf("text is not minified JSON (err=%v)", err)
		}
		if len(env.StructuredContent) == 0 {
			t.Fatal("structuredContent must be present for an object result")
		}
		fromText := decodeObject(t, []byte(text))
		structured := decodeObject(t, env.StructuredContent)
		if !reflect.DeepEqual(fromText, structured) {
			t.Fatalf("structuredContent must deep-equal the parsed text\n text: %v\n  sc:  %v", fromText, structured)
		}
		results, ok := structured["results"].([]interface{})
		if !ok || len(results) != 10 {
			t.Fatalf("results=%v", structured["results"])
		}
		if structured["total"] != float64(25) || structured["nextCursor"] == "" || structured["nextCursor"] == nil {
			t.Fatalf("total/nextCursor wrong: %v %v", structured["total"], structured["nextCursor"])
		}
		for _, gone := range []string{"offset", "hasMore"} {
			if _, present := structured[gone]; present {
				t.Fatalf("%s must be gone from the response", gone)
			}
		}
		if id, _ := structured["resultSetId"].(string); !resultSetIDPattern.MatchString(id) {
			t.Fatalf("resultSetId=%v", structured["resultSetId"])
		}
	})
}

func TestEnvelopeNonSearchObjectToolsEmitStructuredContentToo(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		s := newResultSetServer(t)
		env := mustEnvelope(t, call, s, "get_current_date", map[string]interface{}{})
		text := env.text(t)
		if strings.Contains(text, "\n") {
			t.Fatalf("text must be minified: %q", text)
		}
		if !reflect.DeepEqual(decodeObject(t, []byte(text)), decodeObject(t, env.StructuredContent)) {
			t.Fatalf("structuredContent %s != text %s", env.StructuredContent, text)
		}
	})
}

func TestEnvelopeStructuredContentOmittedForNonObjectResults(t *testing.T) {
	cases := map[string]struct {
		value    interface{}
		wantText string
	}{
		"array":  {[]string{"a", "b"}, `["a","b"]`},
		"string": {"plain", `"plain"`},
		"nil":    {nil, `null`},
	}
	for name, tc := range cases {
		forEachPath(t, func(t *testing.T, call toolCaller) {
			s := newResultSetServer(t)
			value := tc.value
			s.toolsRegistry = append(s.toolsRegistry, Tool{Name: "test_non_object", Handler: func(context.Context, json.RawMessage) (interface{}, error) {
				return value, nil
			}})
			env := mustEnvelope(t, call, s, "test_non_object", map[string]interface{}{})
			if len(env.StructuredContent) != 0 {
				t.Fatalf("%s: structuredContent must be absent for a non-object result, got %s", name, env.StructuredContent)
			}
			if got := env.text(t); got != tc.wantText {
				t.Fatalf("%s: text=%q want %q", name, got, tc.wantText)
			}
		})
	}
}

func TestEnvelopeCompactFormatKeepsFullStructuredContent(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		fake := newSearxngFake(t, map[string][]string{"1": numbered("a", 8)})
		s := newWebServer(t, fake)

		snippets := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "compact q", "maxResults": 3, "format": "compact"})
		text := snippets.text(t)
		if strings.HasPrefix(strings.TrimSpace(text), "{") {
			t.Fatalf("compact text must not be JSON: %.120q", text)
		}
		for _, want := range []string{"query: compact q", "resultSetId=rs_", "total=8", "nextCursor=", "1. t0 | https://example.test/a/0 | c0", "3. t2 | https://example.test/a/2 | c2"} {
			if !strings.Contains(text, want) {
				t.Fatalf("compact text missing %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "offset=") || strings.Contains(text, "hasMore") {
			t.Fatalf("compact header must not carry offset/hasMore:\n%s", text)
		}
		sc := decodeObject(t, snippets.StructuredContent)
		results, _ := sc["results"].([]interface{})
		first, _ := results[0].(map[string]interface{})
		if len(results) != 3 || first["snippet"] != "c0" || first["engine"] != "bing" || sc["nextCursor"] == nil {
			t.Fatalf("structuredContent must stay the full typed response: %v", sc)
		}

		titles := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "compact q", "maxResults": 3, "format": "compact", "detail": "titles"})
		if tt := titles.text(t); !strings.Contains(tt, "1. t0 | https://example.test/a/0") || strings.Contains(tt, "c0") {
			t.Fatalf("detail=titles must drop snippets:\n%s", tt)
		}
		sc = decodeObject(t, titles.StructuredContent)
		first = sc["results"].([]interface{})[0].(map[string]interface{})
		if first["snippet"] != "c0" {
			t.Fatalf("structuredContent keeps snippets whatever the detail: %v", first)
		}

		full := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "compact q", "maxResults": 3, "format": "compact", "detail": "full"})
		if ft := full.text(t); !strings.Contains(ft, "engine=bing") {
			t.Fatalf("detail=full must add optional fields:\n%s", ft)
		}

		// detail without format keeps the JSON default.
		jsonDefault := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "compact q", "maxResults": 3, "detail": "titles"})
		if !strings.HasPrefix(jsonDefault.text(t), "{") {
			t.Fatalf("format defaults to json: %.80q", jsonDefault.text(t))
		}
		explicit := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "compact q", "maxResults": 3, "format": "json"})
		// Each call gets its own result set (cache hits keep the cached flag),
		// so compare everything except those per-call fields.
		strip := func(text string) map[string]interface{} {
			m := decodeObject(t, []byte(text))
			for _, k := range []string{"resultSetId", "nextCursor", "cached"} {
				delete(m, k)
			}
			return m
		}
		if strings.Contains(explicit.text(t), "\n") || !reflect.DeepEqual(strip(explicit.text(t)), strip(jsonDefault.text(t))) {
			t.Fatalf("format=json must equal the default text")
		}
	})
}

func TestEnvelopeRejectsUnknownFormatAndDetailBeforeSearching(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		fake := &fakeResultsConnector{name: "web", n: 5}
		s := newResultSetServer(t, fake)
		cases := []struct {
			args map[string]interface{}
			want []string
		}{
			{map[string]interface{}{"query": "q", "format": "xml"}, []string{"format", `"xml"`, `"json"`, `"compact"`}},
			{map[string]interface{}{"query": "q", "format": "JSON"}, []string{"format", `"JSON"`, `"json"`, `"compact"`}},
			{map[string]interface{}{"query": "q", "format": "compact", "detail": "everything"}, []string{"detail", `"everything"`, `"titles"`, `"snippets"`, `"full"`}},
			{map[string]interface{}{"query": "q", "detail": "nope"}, []string{"detail", `"nope"`, `"titles"`, `"snippets"`, `"full"`}},
		}
		for _, tc := range cases {
			msg := mustToolError(t, call, s, "search_web", tc.args)
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Fatalf("%v: error %q must contain %q", tc.args, msg, w)
				}
			}
		}
		if fake.callCount() != 0 || s.resultSets.Stats().Sets != 0 {
			t.Fatalf("rejected arguments must not search or store: calls=%d stats=%+v", fake.callCount(), s.resultSets.Stats())
		}
	})
}

func TestEnvelopeCursorRoundTripCoversTheWholeSetWithoutUpstreamRequests(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		fake := newSearxngFake(t, map[string][]string{"1": numbered("a", 25)})
		s := newWebServer(t, fake)

		page := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "round trip", "maxResults": 10})
		requestsAfterSearch := fake.requestCount()
		sc := decodeObject(t, page.StructuredContent)
		id := sc["resultSetId"].(string)

		var urls []string
		collect := func(sc map[string]interface{}) {
			for _, r := range sc["results"].([]interface{}) {
				urls = append(urls, r.(map[string]interface{})["url"].(string))
			}
		}
		collect(sc)
		sizes := []int{len(sc["results"].([]interface{}))}
		for pages := 0; ; pages++ {
			next, _ := sc["nextCursor"].(string)
			if next == "" {
				break
			}
			if pages > 5 {
				t.Fatal("cursor chain does not terminate")
			}
			page = mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "round trip", "maxResults": 10, "cursor": next})
			sc = decodeObject(t, page.StructuredContent)
			if sc["resultSetId"] != id || sc["total"] != float64(25) {
				t.Fatalf("paging must keep id and total: %v", sc)
			}
			collect(sc)
			sizes = append(sizes, len(sc["results"].([]interface{})))
		}
		// T11: a web set keeps nextCursor at its stored end while upstream pages
		// remain; that last call fetches one SearXNG page, which here is empty.
		if !reflect.DeepEqual(sizes, []int{10, 10, 5, 0}) {
			t.Fatalf("page sizes=%v", sizes)
		}
		for i, u := range urls {
			if u != fmt.Sprintf("https://example.test/a/%d", i) {
				t.Fatalf("order changed at %d: %s", i, u)
			}
		}
		if len(urls) != 25 {
			t.Fatalf("whole set not covered: %d", len(urls))
		}
		if _, present := sc["nextCursor"]; present {
			t.Fatalf("the last page must not carry nextCursor: %v", sc["nextCursor"])
		}
		if fake.requestCount() != requestsAfterSearch+1 {
			t.Fatalf("paging the stored set must be local and past its end exactly one lazy page: %d -> %d", requestsAfterSearch, fake.requestCount())
		}
	})
}

func TestEnvelopeCursorHonorsCallerPageSizeAndCompactFormat(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		fake := &fakeResultsConnector{name: "web", n: 25}
		s := newResultSetServer(t, fake)
		first := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 4})
		cursor := decodeObject(t, first.StructuredContent)["nextCursor"].(string)

		page := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 7, "cursor": cursor, "format": "compact", "detail": "titles"})
		text := page.text(t)
		if !strings.Contains(text, "\n5. web result 4 | ") || !strings.Contains(text, "\n11. web result 10 | ") || strings.Contains(text, "\n12. ") {
			t.Fatalf("page of 7 numbered 5..11 expected:\n%s", text)
		}
		if !strings.Contains(text, "nextCursor=") {
			t.Fatalf("more results remain, header needs nextCursor:\n%s", text)
		}
		if fake.callCount() != 1 {
			t.Fatalf("connector calls=%d", fake.callCount())
		}
	})
}

func TestEnvelopeInvalidCursorIsRejectedWithoutTouchingTheStore(t *testing.T) {
	b64 := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	valid := b64("v1:rs_ABCDEFGHIJKLMNOP:5")
	cases := map[string]string{
		"not base64":          "%%%not-base64%%%",
		"padded":              valid + "=",
		"std alphabet":        strings.NewReplacer("-", "+", "_", "/").Replace(b64("v1:rs_ABCDEFGHIJKLMNOP:\xfb\xff")),
		"garbage payload":     b64("hello"),
		"unknown version":     b64("v9:rs_ABCDEFGHIJKLMNOP:5"),
		"bad id shape":        b64("v1:rs_short:5"),
		"id with lowercase":   b64("v1:rs_abcdefghijklmnop:5"),
		"negative offset":     b64("v1:rs_ABCDEFGHIJKLMNOP:-1"),
		"non numeric offset":  b64("v1:rs_ABCDEFGHIJKLMNOP:five"),
		"signed offset":       b64("v1:rs_ABCDEFGHIJKLMNOP:+5"),
		"leading zero offset": b64("v1:rs_ABCDEFGHIJKLMNOP:05"),
		"huge offset":         b64("v1:rs_ABCDEFGHIJKLMNOP:99999999999999999999"),
		"missing offset":      b64("v1:rs_ABCDEFGHIJKLMNOP"),
		"extra field":         b64("v1:rs_ABCDEFGHIJKLMNOP:5:6"),
		"truncated":           valid[:len(valid)-3],
	}
	forEachPath(t, func(t *testing.T, call toolCaller) {
		fake := &fakeResultsConnector{name: "web", n: 12}
		s := newResultSetServer(t, fake)
		seed := mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 5})
		_ = seed
		before := s.resultSets.Stats()
		for name, cursor := range cases {
			msg := mustToolError(t, call, s, "search_web", map[string]interface{}{"query": "q", "cursor": cursor})
			if !strings.HasPrefix(msg, "invalid_cursor: ") || len(msg) <= len("invalid_cursor: ") {
				t.Errorf("%s: want invalid_cursor: <reason>, got %q", name, msg)
			}
			if strings.HasPrefix(msg, "result_set_not_found") {
				t.Errorf("%s: a malformed cursor must not reach the store lookup", name)
			}
		}
		if after := s.resultSets.Stats(); after != before {
			t.Fatalf("invalid cursors changed the store: %+v -> %+v", before, after)
		}
		if fake.callCount() != 1 {
			t.Fatalf("invalid cursors must not search; calls=%d", fake.callCount())
		}
	})
}

func TestEnvelopeWellFormedCursorOfUnknownSetKeepsTheExactNotFoundText(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		fake := &fakeResultsConnector{name: "web", n: 3}
		s := newResultSetServer(t, fake)
		msg := mustToolError(t, call, s, "search_web", map[string]interface{}{"query": "q", "cursor": encodeCursor("search_web", "rs_AAAAAAAAAAAAAAAA", 0)})
		want := "result_set_not_found: rs_AAAAAAAAAAAAAAAA (expired or unknown; run the search again)"
		if msg != want {
			t.Fatalf("error=%q want %q", msg, want)
		}
	})
}

func TestEnvelopeCursorIsOpaqueButRoundTrips(t *testing.T) {
	c := encodeCursor("search_web", "rs_ABCDEFGHIJKLMNOP", 20)
	if strings.ContainsAny(c, "=+/ ") || strings.Contains(c, "rs_ABC") {
		t.Fatalf("cursor must be opaque base64url without padding: %q", c)
	}
	tool, id, off, err := decodeCursor(c)
	if err != nil || tool != "search_web" || id != "rs_ABCDEFGHIJKLMNOP" || off != 20 {
		t.Fatalf("decode = %q %q %d %v", tool, id, off, err)
	}
}

// Regression for the aliasing found at tools.go:~123: the planner's
// "intent: ..." warning must never be written into the spare capacity of the
// warnings slice a connector returned.
func TestSearchHandlerDoesNotWriteIntentWarningIntoConnectorWarningsStorage(t *testing.T) {
	warnings := make([]string, 3, 8)
	for i := range warnings {
		warnings[i] = fmt.Sprintf("connector warning %d", i)
	}
	fake := &fakeResultsConnector{name: "web", n: 3, warnings: warnings}
	s := newResultSetServer(t, fake)
	env := mustEnvelope(t, callPaths["HandleToolsCall"], s, "search_web", map[string]interface{}{"query": "latest news about golang"})

	got := decodeObject(t, []byte(env.text(t)))["warnings"].([]interface{})
	sawIntent := false
	for _, w := range got {
		if strings.HasPrefix(w.(string), "intent: ") {
			sawIntent = true
		}
	}
	if !sawIntent {
		t.Fatalf("fixture needs the planner to add an intent warning; warnings=%v", got)
	}
	if spare := warnings[:cap(warnings)][3]; spare != "" {
		t.Fatalf("the connector's warnings backing array was written to: %q", spare)
	}
	if len(warnings) != 3 {
		t.Fatalf("connector warnings changed length: %v", warnings)
	}
}

func TestSearchToolsAdvertiseFormatDetailAndCursor(t *testing.T) {
	checked := 0
	for name, tool := range listedTools(t) {
		schema, _ := tool["inputSchema"].(map[string]interface{})
		props, _ := schema["properties"].(map[string]interface{})
		if !strings.HasPrefix(name, "search_") {
			for _, p := range []string{"format", "detail", "cursor"} {
				if _, present := props[p]; present {
					t.Errorf("%s must not advertise %s", name, p)
				}
			}
			continue
		}
		checked++
		enumOf := func(prop string) []interface{} {
			p, _ := props[prop].(map[string]interface{})
			e, _ := p["enum"].([]interface{})
			return e
		}
		if got := enumOf("format"); !reflect.DeepEqual(got, []interface{}{"json", "compact"}) {
			t.Errorf("%s format enum=%v", name, got)
		}
		if got := enumOf("detail"); !reflect.DeepEqual(got, []interface{}{"titles", "snippets", "full"}) {
			t.Errorf("%s detail enum=%v", name, got)
		}
		desc := func(prop string) string {
			p, _ := props[prop].(map[string]interface{})
			d, _ := p["description"].(string)
			return d
		}
		if c, _ := props["cursor"].(map[string]interface{}); c["type"] != "string" || !strings.Contains(desc("cursor"), "nextCursor") {
			t.Errorf("%s cursor must be a string described via nextCursor: %v", name, props["cursor"])
		}
		if !strings.Contains(strings.ToLower(desc("maxResults")), "tamaño de página") {
			t.Errorf("%s maxResults must be described as the page size chosen by the caller: %q", name, desc("maxResults"))
		}
		all := desc("format") + " " + desc("detail") + " " + desc("maxResults") + " " + desc("cursor")
		for _, want := range []string{"format=compact", "detail=titles", "trunc"} {
			if !strings.Contains(all, want) {
				t.Errorf("%s descriptions must advise large pages to use compact titles (missing %q): %s", name, want, all)
			}
		}
		for _, gone := range []string{"offset", "resultSetId", "hasMore"} {
			if strings.Contains(all, gone) {
				t.Errorf("%s descriptions must not mention the retired %s: %s", name, gone, all)
			}
		}
	}
	if checked != 16 {
		t.Fatalf("expected 16 search tools, got %d", checked)
	}
}

// The default text is the former indented JSON, minified: same value, fewer
// bytes. The sizes are logged for the record (go test -v).
func TestEnvelopeDefaultTextIsSmallerThanTheFormerIndentedJSON(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 20}
	s := newResultSetServer(t, fake)
	env := mustEnvelope(t, callPaths["HandleToolsCall"], s, "search_web", map[string]interface{}{"query": "size", "maxResults": 20})
	text := env.text(t)

	var value interface{}
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatal(err)
	}
	indented, _ := json.MarshalIndent(value, "", "  ")
	t.Logf("default text: %d bytes minified vs %d bytes indented (%.0f%%)", len(text), len(indented), 100*float64(len(text))/float64(len(indented)))
	if len(text) >= len(indented) {
		t.Fatalf("minified text (%d) must be smaller than indented (%d)", len(text), len(indented))
	}
}
