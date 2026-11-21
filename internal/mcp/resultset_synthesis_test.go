package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// callSynth runs a tools/call and returns the raw result map.
func callSynth(t *testing.T, s *Server, tool string, args map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, _ := json.Marshal(args)
	params, _ := json.Marshal(map[string]interface{}{"name": tool, "arguments": json.RawMessage(raw)})
	res, err := s.HandleToolsCall(context.Background(), params)
	if err != nil {
		t.Fatalf("%s: unexpected JSON-RPC error: %v", tool, err)
	}
	return res.(map[string]interface{})
}

func resultText(t *testing.T, res map[string]interface{}) string {
	t.Helper()
	content := res["content"].([]map[string]interface{})
	return content[0]["text"].(string)
}

var synthesisToolsByID = []string{"summarize_results", "deep_research", "compare_sources"}

func TestSynthesisResolvesResultSetIDWithoutNetwork(t *testing.T) {
	for _, tool := range synthesisToolsByID {
		t.Run(tool, func(t *testing.T) {
			fake := &fakeResultsConnector{name: "npm", n: 6}
			s := newResultSetServer(t, fake)
			first := mustPaged(t, s, "search_npm", map[string]interface{}{"query": "synth-" + tool})
			if fake.callCount() != 1 {
				t.Fatalf("setup search should call the connector once, got %d", fake.callCount())
			}

			byID := callSynth(t, s, tool, map[string]interface{}{"query": "synth-" + tool, "resultSetId": first.ResultSetID})
			if byID["isError"] == true {
				t.Fatalf("resultSetId call failed: %s", resultText(t, byID))
			}
			byResults := callSynth(t, s, tool, map[string]interface{}{"query": "synth-" + tool, "results": first.Results})
			if got, want := resultText(t, byID), resultText(t, byResults); got != want {
				t.Fatalf("resultSetId synthesis must equal synthesis of the same results:\n id: %s\nres: %s", got, want)
			}
			if strings.Contains(resultText(t, byID), "No results") || strings.Contains(resultText(t, byID), "No sources") {
				t.Fatalf("stored results were not resolved: %s", resultText(t, byID))
			}
			if _, has := byID["structuredContent"].(json.RawMessage); !has {
				t.Fatalf("expected structuredContent, got %#v", byID["structuredContent"])
			}
			if strings.Contains(resultText(t, byID), `"warnings"`) {
				t.Fatalf("a clean resolution must not add warnings: %s", resultText(t, byID))
			}
			if fake.callCount() != 1 {
				t.Fatalf("synthesis by ID must not search again, connector calls = %d", fake.callCount())
			}
		})
	}
}

func TestSynthesisResultsWinOverResultSetIDWithWarning(t *testing.T) {
	for _, tool := range synthesisToolsByID {
		t.Run(tool, func(t *testing.T) {
			s := newResultSetServer(t, &fakeResultsConnector{name: "npm", n: 6})
			first := mustPaged(t, s, "search_npm", map[string]interface{}{"query": "both-" + tool})
			one := first.Results[:1]

			res := callSynth(t, s, tool, map[string]interface{}{"query": "both-" + tool, "results": one, "resultSetId": first.ResultSetID})
			if res["isError"] == true {
				t.Fatalf("both inputs must not fail: %s", resultText(t, res))
			}
			var out map[string]interface{}
			if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			warnings, _ := out["warnings"].([]interface{})
			want := "resultSetId ignored: results were provided in the same call; results win"
			if len(warnings) != 1 || warnings[0] != want {
				t.Fatalf("expected exactly warning %q, got %v", want, out["warnings"])
			}

			// The synthesized content is the one built from `results`.
			plain := callSynth(t, s, tool, map[string]interface{}{"query": "both-" + tool, "results": one})
			var plainOut map[string]interface{}
			_ = json.Unmarshal([]byte(resultText(t, plain)), &plainOut)
			delete(out, "warnings")
			a, _ := json.Marshal(out)
			b, _ := json.Marshal(plainOut)
			if string(a) != string(b) {
				t.Fatalf("results must win:\n got %s\nwant %s", a, b)
			}
		})
	}
}

func TestSynthesisUnknownResultSetIDIsToolError(t *testing.T) {
	for _, tool := range synthesisToolsByID {
		t.Run(tool, func(t *testing.T) {
			s := newResultSetServer(t, &fakeResultsConnector{name: "npm", n: 3})
			id := "rs_AAAAAAAAAAAAAAAA"
			res := callSynth(t, s, tool, map[string]interface{}{"query": "q", "resultSetId": id})
			if res["isError"] != true {
				t.Fatalf("unknown ID must be an isError result, got %#v", res)
			}
			want := "result_set_not_found: " + id + " (expired or unknown; run the search again)"
			if got := resultText(t, res); got != want {
				t.Fatalf("error text = %q, want %q", got, want)
			}
			if _, has := res["structuredContent"]; has {
				t.Fatalf("an error result carries no structuredContent")
			}
		})
	}
}

func TestSynthesisWithoutResultSetIDKeepsPreviousBehavior(t *testing.T) {
	s := newResultSetServer(t, &fakeResultsConnector{name: "npm", n: 3})
	empty := callSynth(t, s, "summarize_results", map[string]interface{}{"query": "q"})
	if empty["isError"] == true || !strings.Contains(resultText(t, empty), "No results to synthesize.") {
		t.Fatalf("empty results without an ID must keep returning the empty synthesis: %s", resultText(t, empty))
	}
	if strings.Contains(resultText(t, empty), `"warnings"`) {
		t.Fatalf("no warnings expected: %s", resultText(t, empty))
	}
}

func TestSynthesisInputSchemaAdvertisesResultSetID(t *testing.T) {
	s := newResultSetServer(t)
	seen := 0
	for _, tool := range s.toolsRegistry {
		for _, name := range synthesisToolsByID {
			if tool.Name != name {
				continue
			}
			seen++
			props := tool.InputSchema["properties"].(map[string]interface{})
			prop, ok := props["resultSetId"].(map[string]interface{})
			if !ok || prop["type"] != "string" {
				t.Fatalf("%s must declare resultSetId as a string, got %#v", name, props["resultSetId"])
			}
			if req, _ := tool.InputSchema["required"].([]string); len(req) != 1 || req[0] != "query" {
				t.Fatalf("%s must keep only query required, got %v", name, req)
			}
		}
	}
	if seen != 3 {
		t.Fatalf("expected 3 synthesis tools, saw %d", seen)
	}
}
