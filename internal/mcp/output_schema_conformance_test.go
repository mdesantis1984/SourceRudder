package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// validateSchema checks value against the JSON Schema subset the server's
// outputSchema uses: type (string or list), properties, required, items and
// enum. Unknown keywords are ignored; additional properties are allowed.
func validateSchema(schema map[string]interface{}, value interface{}, path string) []string {
	var problems []string
	if want, ok := schema["type"]; ok {
		var types []string
		switch w := want.(type) {
		case string:
			types = []string{w}
		case []interface{}:
			for _, x := range w {
				types = append(types, x.(string))
			}
		case []string:
			types = w
		}
		if !matchesAnyType(types, value) {
			return []string{fmt.Sprintf("%s: %s is not of type %v", path, describe(value), types)}
		}
	}
	if enum, ok := schema["enum"].([]interface{}); ok {
		found := false
		for _, e := range enum {
			if e == value {
				found = true
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("%s: %v is not in the enum", path, value))
		}
	}
	switch v := value.(type) {
	case map[string]interface{}:
		for _, r := range toStrings(schema["required"]) {
			if _, ok := v[r]; !ok {
				problems = append(problems, fmt.Sprintf("%s: missing required property %q", path, r))
			}
		}
		if props, ok := schema["properties"].(map[string]interface{}); ok {
			keys := make([]string, 0, len(v))
			for k := range v {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if sub, ok := props[k].(map[string]interface{}); ok {
					problems = append(problems, validateSchema(sub, v[k], path+"."+k)...)
				}
			}
		}
	case []interface{}:
		if items, ok := schema["items"].(map[string]interface{}); ok {
			for i, e := range v {
				problems = append(problems, validateSchema(items, e, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}
	return problems
}

func matchesAnyType(types []string, v interface{}) bool {
	for _, t := range types {
		switch t {
		case "object":
			if _, ok := v.(map[string]interface{}); ok {
				return true
			}
		case "array":
			if _, ok := v.([]interface{}); ok {
				return true
			}
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "null":
			if v == nil {
				return true
			}
		case "number":
			if _, ok := v.(float64); ok {
				return true
			}
		case "integer":
			if f, ok := v.(float64); ok && f == float64(int64(f)) {
				return true
			}
		}
	}
	return false
}

func describe(v interface{}) string {
	wire, _ := json.Marshal(v)
	if len(wire) > 60 {
		wire = wire[:60]
	}
	return string(wire)
}

func TestValidateSchemaCatchesTheViolationsItExists_To_Catch(t *testing.T) {
	schema := map[string]interface{}{
		"type": "object", "required": []string{"a", "list"},
		"properties": map[string]interface{}{
			"a":    map[string]interface{}{"type": "string"},
			"n":    map[string]interface{}{"type": "integer"},
			"list": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "required": []string{"x"}}},
		},
	}
	good := map[string]interface{}{"a": "s", "n": 3.0, "list": []interface{}{map[string]interface{}{"x": 1.0}}, "extra": true}
	if p := validateSchema(schema, good, "$"); len(p) != 0 {
		t.Fatalf("valid value rejected: %v", p)
	}
	bad := map[string]interface{}{"a": 1.0, "n": 1.5, "list": []interface{}{map[string]interface{}{}}}
	got := strings.Join(validateSchema(schema, bad, "$"), "\n")
	for _, want := range []string{"$.a:", "$.n:", "$.list[0]: missing required property \"x\""} {
		if !strings.Contains(got, want) {
			t.Errorf("problems %q lack %q", got, want)
		}
	}
	if p := validateSchema(schema, map[string]interface{}{"a": "s", "list": nil}, "$"); len(p) != 1 || !strings.Contains(p[0], "$.list") {
		t.Errorf("null array must violate the array type: %v", p)
	}
	if p := validateSchema(schema, map[string]interface{}{"a": "s"}, "$"); len(p) != 1 || !strings.Contains(p[0], `"list"`) {
		t.Errorf("missing required not reported: %v", p)
	}
}

// richConnector returns one fully populated result so every optional field
// of the wire contract is exercised against the declared schema.
type richConnector struct {
	name string
	n    int
}

func (r *richConnector) Name() string { return r.name }
func (r *richConnector) Search(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	return r.build(req), nil
}
func (r *richConnector) SearchPR(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	return r.build(req), nil
}
func (r *richConnector) SearchIssue(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	return r.build(req), nil
}
func (r *richConnector) build(req *types.SearchRequest) *types.SearchResponse {
	published := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	items := make([]types.SearchResultItem, r.n)
	for i := range items {
		items[i] = types.SearchResultItem{
			Title: fmt.Sprintf("%s %d", r.name, i), URL: fmt.Sprintf("https://example.test/%s/%d", r.name, i), Source: r.name,
			Snippet: "snippet", Engine: "duckduckgo", Type: "article", Score: float64(r.n - i), PublishedAt: &published,
			Author: "someone", Tags: []string{"go", "mcp"}, CitationID: "c1", CanonicalURL: "https://example.test/c",
		}
	}
	return &types.SearchResponse{
		Query: req.Query, Results: items, Summary: "sum", KeyFindings: []string{"k"}, SourcesUsed: []string{r.name},
		Strategy: "test", Confidence: 0.5, Cached: true, Partial: true, Warnings: []string{"w"}, Errors: []string{"e"},
	}
}

// declaredSchemas returns name -> outputSchema exactly as tools/list serves it.
func declaredSchemas(t *testing.T, s *Server) map[string]map[string]interface{} {
	t.Helper()
	out := map[string]map[string]interface{}{}
	for _, tool := range listPaths["HTTP /mcp"](t, s) {
		if schema, ok := tool["outputSchema"].(map[string]interface{}); ok {
			out[tool["name"].(string)] = schema
		}
	}
	return out
}

func conformanceFixtures() map[string][]map[string]interface{} {
	return map[string][]map[string]interface{}{
		"populated": {{"query": "q", "maxResults": 3}},
		"paged":     {{"query": "q", "maxResults": 2}},
		"compact":   {{"query": "q", "format": "compact", "detail": "titles"}},
	}
}

func TestEverySearchToolStructuredContentConformsToItsOutputSchema(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		var conns []types.SearchConnector
		seen := map[string]bool{}
		for _, c := range searchToolConnectors {
			if !seen[c] {
				seen[c] = true
				conns = append(conns, &richConnector{name: c, n: 5})
			}
		}
		s := newResultSetServer(t, conns...)
		schemas := declaredSchemas(t, s)
		checked := 0
		for tool := range searchToolConnectors {
			schema, ok := schemas[tool]
			if !ok {
				t.Errorf("%s: no outputSchema declared", tool)
				continue
			}
			for fixture, argSets := range conformanceFixtures() {
				for _, args := range argSets {
					env := mustEnvelope(t, call, s, tool, args)
					checkConforms(t, tool+"/"+fixture, schema, env)
					checked++
					// follow the cursor: paged slices must conform too.
					if sc := decodeObject(t, env.StructuredContent); sc["nextCursor"] != nil {
						next := mustEnvelope(t, call, s, tool, map[string]interface{}{"query": "q", "cursor": sc["nextCursor"], "maxResults": 2})
						checkConforms(t, tool+"/cursor page", schema, next)
						checked++
					}
				}
			}
		}
		if checked < len(searchToolConnectors)*4 {
			t.Fatalf("only %d outputs checked", checked)
		}
	})
}

func TestSearchOutputsWithoutConnectorsOrResultsStillConform(t *testing.T) {
	forEachPath(t, func(t *testing.T, call toolCaller) {
		s := newResultSetServer(t, &fakeResultsConnector{name: "web", n: 0})
		schema := declaredSchemas(t, s)["search_web"]
		checkConforms(t, "zero results", schema, mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "nothing"}))
		checkConforms(t, "unregistered source", schema, mustEnvelope(t, call, s, "search_npm", map[string]interface{}{"query": "nothing"}))
		s.connectorManager = nil
		checkConforms(t, "no connector manager", schema, mustEnvelope(t, call, s, "search_web", map[string]interface{}{"query": "nothing"}))
	})
}

func TestEverySynthesisToolStructuredContentConformsToItsOutputSchema(t *testing.T) {
	results := []map[string]interface{}{
		{"title": "Go modules", "url": "https://go.dev/ref/mod", "source": "web", "snippet": "Modules are how Go manages dependencies.", "score": 3.0, "publishedAt": "2026-01-02T03:04:05Z", "tags": []string{"go"}},
		{"title": "Go modules tutorial", "url": "https://go.dev/doc/tutorial", "source": "web", "snippet": "Create a module and manage dependencies in Go.", "score": 2.0},
		{"title": "Module cache", "url": "https://go.dev/ref/cache", "source": "github", "snippet": "Dependencies are cached.", "score": 1.0},
	}
	argSets := map[string]map[string]interface{}{
		"three results": {"query": "go modules", "results": results},
		"one result":    {"query": "go modules", "results": results[:1]},
		"no results":    {"query": "go modules", "results": []interface{}{}},
		"no arguments":  {},
	}
	forEachPath(t, func(t *testing.T, call toolCaller) {
		s := newResultSetServer(t)
		schemas := declaredSchemas(t, s)
		for _, tool := range synthesisTools {
			schema, ok := schemas[tool]
			if !ok {
				t.Errorf("%s: no outputSchema declared", tool)
				continue
			}
			for name, args := range argSets {
				checkConforms(t, tool+"/"+name, schema, mustEnvelope(t, call, s, tool, args))
			}
		}
		// the stub answer used when the synthesis service is not wired.
		s.synthesisService = nil
		for _, tool := range synthesisTools {
			checkConforms(t, tool+"/stub", schemas[tool], mustEnvelope(t, call, s, tool, argSets["three results"]))
		}
	})
}

func checkConforms(t *testing.T, label string, schema map[string]interface{}, env envelope) {
	t.Helper()
	if schema == nil {
		t.Errorf("%s: no schema", label)
		return
	}
	if len(env.StructuredContent) == 0 {
		t.Errorf("%s: structuredContent missing although the tool declares an outputSchema (MCP: MUST conform)", label)
		return
	}
	var value interface{}
	if err := json.Unmarshal(env.StructuredContent, &value); err != nil {
		t.Errorf("%s: structuredContent is not JSON: %v", label, err)
		return
	}
	if problems := validateSchema(schema, value, "$"); len(problems) > 0 {
		t.Errorf("%s: structuredContent violates its outputSchema:\n  %s\n  value: %.300s", label, strings.Join(problems, "\n  "), env.StructuredContent)
	}
}
