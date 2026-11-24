package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// listedTools returns tools/list exactly as a client receives it
// (marshalled and decoded), keyed by tool name.
func listedTools(t *testing.T) map[string]map[string]interface{} {
	t.Helper()
	s := buildTestServer(t)
	res, err := s.HandleToolsList(context.Background(), nil)
	if err != nil {
		t.Fatalf("HandleToolsList: %v", err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal tools/list: %v", err)
	}
	var decoded struct {
		Tools []map[string]interface{} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}
	out := make(map[string]map[string]interface{}, len(decoded.Tools))
	for _, tool := range decoded.Tools {
		name, _ := tool["name"].(string)
		out[name] = tool
	}
	return out
}

func listedDescription(t *testing.T, name string) string {
	t.Helper()
	tool, ok := listedTools(t)[name]
	if !ok {
		t.Fatalf("tool %q missing from tools/list", name)
	}
	desc, _ := tool["description"].(string)
	return desc
}

// S4: search_news must not claim a filter the code never applies.
func TestToolsListSearchNewsDoesNotClaimPlannerTimeRange(t *testing.T) {
	desc := listedDescription(t, "search_news")
	lower := strings.ToLower(desc)
	for _, banned := range []string{"timerange=week", "planner", "hereda"} {
		if strings.Contains(lower, banned) {
			t.Errorf("search_news description must not contain %q; got %q", banned, desc)
		}
	}
	if !strings.Contains(desc, "SearxNG") {
		t.Errorf("search_news description must keep mentioning its SearxNG backend; got %q", desc)
	}
}

// S4: the synthesis tools do not search; they only process `results`.
func TestToolsListSynthesisToolsStateTheyDoNotSearch(t *testing.T) {
	for _, name := range []string{"deep_research", "summarize_results", "compare_sources"} {
		desc := listedDescription(t, name)
		lower := strings.ToLower(desc)
		for _, want := range []string{"no busca", "solo sintetiza", "results", "search_"} {
			if !strings.Contains(lower, want) {
				t.Errorf("%s description must contain %q; got %q", name, want, desc)
			}
		}
	}
}

// S4: the maxResults schema text states the real default and cap, and
// never attributes it to the planner.
func TestToolsListMaxResultsDescriptionStatesRealDefaultAndCap(t *testing.T) {
	checked := 0
	for name, tool := range listedTools(t) {
		schema, _ := tool["inputSchema"].(map[string]interface{})
		props, _ := schema["properties"].(map[string]interface{})
		prop, ok := props["maxResults"].(map[string]interface{})
		if !ok {
			continue
		}
		checked++
		desc, _ := prop["description"].(string)
		if strings.Contains(strings.ToLower(desc), "planner") {
			t.Errorf("%s maxResults description must not mention the planner; got %q", name, desc)
		}
		for _, want := range []string{"default 10", "20 en search_web", "tope 100"} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s maxResults description must contain %q; got %q", name, want, desc)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no tool in tools/list exposes maxResults")
	}
}

// Regression: the tool count is unchanged by the description fixes.
func TestToolsListCountUnchangedByDescriptionFixes(t *testing.T) {
	if got := len(listedTools(t)); got != 30 {
		t.Fatalf("tools/list must still expose 30 tools, got %d", got)
	}
}
