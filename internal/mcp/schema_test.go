package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSearchInputSchemaHasTimeRangeEnum locks the schema promise: the
// timeRange field is constrained to SearxNG's known set ("" / day /
// week / month / year). AI agents that see a stable set can safely
// autocomplete instead of guessing "monthly" / "1week" / etc., which
// SearxNG would silently ignore or 400 on.
func TestSearchInputSchemaHasTimeRangeEnum(t *testing.T) {
	schema := searchInputSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	tr, ok := props["timeRange"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected timeRange property, got %T", props["timeRange"])
	}
	enum, ok := tr["enum"].([]string)
	if !ok {
		t.Fatalf("expected timeRange enum to be []string, got %T", tr["enum"])
	}
	want := map[string]bool{"": true, "day": true, "week": true, "month": true, "year": true}
	for _, v := range enum {
		if !want[v] {
			t.Errorf("unexpected enum value %q in timeRange; the enum must match SearxNG's accepted set", v)
		}
	}
	if len(enum) != len(want) {
		t.Errorf("expected %d timeRange enum values, got %d (%v)", len(want), len(enum), enum)
	}
}

// TestFetchAndExtractInputSchemaHasModeEnum locks the contract that
// fetch_and_extract exposes the four-mode enum (auto / article /
// documentation / raw) explicitly. The other fetch tools (fetch_url,
// extract_structured) must NOT advertise mode as an input because the
// handlers ignore it.
func TestFetchAndExtractInputSchemaHasModeEnum(t *testing.T) {
	schema := fetchAndExtractInputSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	mode, ok := props["mode"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected mode property, got %T", props["mode"])
	}
	enum, ok := mode["enum"].([]string)
	if !ok {
		t.Fatalf("expected mode enum to be []string, got %T", mode["enum"])
	}
	want := map[string]bool{"": true, "auto": true, "article": true, "documentation": true, "raw": true}
	for _, v := range enum {
		if !want[v] {
			t.Errorf("unexpected enum value %q in mode", v)
		}
	}
	if len(enum) != len(want) {
		t.Errorf("expected %d mode enum values, got %d (%v)", len(want), len(enum), enum)
	}
}

// TestFetchURLInputSchemaOmitsModeAndTimeoutMs proves the schema no
// longer advertises fields the handler discards. If a future edit
// re-adds timeoutMs, this test fires so agents do not start sending
// a value the server silently ignores.
func TestFetchURLInputSchemaOmitsModeAndTimeoutMs(t *testing.T) {
	schema := fetchURLInputSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	if _, has := props["mode"]; has {
		t.Errorf("fetch_url schema must NOT advertise mode (the handler ignores it); got %v", props["mode"])
	}
	if _, has := props["timeoutMs"]; has {
		t.Errorf("fetch_url schema must NOT advertise timeoutMs (the handler ignores it; the server uses the boot-time --fetch-timeout-ms flag); got %v", props["timeoutMs"])
	}
	if _, has := props["url"]; !has {
		t.Errorf("fetch_url schema must require url")
	}
	required, ok := schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "url" {
		t.Errorf("expected required=[url], got %v", schema["required"])
	}
}

// TestExtractStructuredInputSchemaOmitsModeAndTimeoutMs mirrors the
// fetch_url schema test: extract_structured also does not honor mode
// or timeoutMs, so the schema must not advertise them.
func TestExtractStructuredInputSchemaOmitsModeAndTimeoutMs(t *testing.T) {
	schema := fetchURLInputSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	for _, banned := range []string{"mode", "timeoutMs"} {
		if _, has := props[banned]; has {
			t.Errorf("extract_structured schema must NOT advertise %s; got %v", banned, props[banned])
		}
	}
}

// TestSynthesisInputSchemaOmitsStyleAndGoal proves the synthesis
// schema no longer advertises fields the synthesis handlers ignore.
// Agents that send style / goal would otherwise burn tokens and add
// noise without any effect on the output.
func TestSynthesisInputSchemaOmitsStyleAndGoal(t *testing.T) {
	schema := synthesisInputSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	for _, banned := range []string{"style", "goal"} {
		if _, has := props[banned]; has {
			t.Errorf("synthesis schema must NOT advertise %s (the handlers ignore it); got %v", banned, props[banned])
		}
	}
	if _, has := props["query"]; !has {
		t.Errorf("synthesis schema must require query")
	}
	required, ok := schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "query" {
		t.Errorf("expected required=[query], got %v", schema["required"])
	}
}

// TestSynthesisResultsArrayAdvertisesSearchResultItemShape proves the
// results array in the synthesis schema points at the SearchResultItem
// schema so agents composing inputs know exactly which fields to send.
// Without this, an agent would have to guess shape from server logs.
func TestSynthesisResultsArrayAdvertisesSearchResultItemShape(t *testing.T) {
	schema := synthesisInputSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	results, ok := props["results"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected results property, got %T", props["results"])
	}
	if typ, _ := results["type"].(string); typ != "array" {
		t.Errorf("expected results.type=array, got %v", results["type"])
	}
	items, ok := results["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected results.items to be a schema object, got %T", results["items"])
	}
	// Walk the schema for the canonical fields an AI agent needs to
	// compose an input. If any of them vanish, the synthesis tools
	// would still parse a payload that violates the documented shape.
	for _, field := range []string{"title", "url", "snippet", "source", "score", "publishedAt", "author", "citationId"} {
		itemProps, ok := items["properties"].(map[string]interface{})
		if !ok {
			t.Fatalf("expected items.properties map, got %T", items["properties"])
		}
		if _, has := itemProps[field]; !has {
			t.Errorf("SearchResultItem schema must expose %q so agents can compose inputs; got %v", field, itemProps)
		}
	}
	required, ok := items["required"].([]string)
	if !ok {
		t.Fatalf("expected SearchResultItem required to be []string, got %T", items["required"])
	}
	want := map[string]bool{"title": true, "url": true, "source": true}
	for _, r := range required {
		if !want[r] {
			t.Errorf("unexpected required field %q on SearchResultItem", r)
		}
	}
}

// TestGitHubFiltersSchemaExposesStateEnum proves the GitHub-specific
// tools surface filters.state as an enum, not a free-form string.
// Agents that send "OPEN" or "Closed" instead of "open" / "closed"
// would have wasted an HTTP call.
func TestGitHubFiltersSchemaExposesStateEnum(t *testing.T) {
	schema := githubFiltersInputSchema()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	filters, ok := props["filters"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected filters property on GitHub schema, got %T", props["filters"])
	}
	filterProps, ok := filters["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected filters.properties, got %T", filters["properties"])
	}
	state, ok := filterProps["state"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected filters.state, got %T", filterProps["state"])
	}
	enum, ok := state["enum"].([]string)
	if !ok {
		t.Fatalf("expected state enum to be []string, got %T", state["enum"])
	}
	want := map[string]bool{"open": true, "closed": true}
	if len(enum) != len(want) {
		t.Errorf("expected %d state enum values, got %d (%v)", len(want), len(enum), enum)
	}
	for _, v := range enum {
		if !want[v] {
			t.Errorf("unexpected enum value %q in filters.state", v)
		}
	}
}

// TestSearchInputSchemaQueryIsRequired locks the contract that every
// search tool requires `query` so agents cannot accidentally call
// search_web without arguments.
func TestSearchInputSchemaQueryIsRequired(t *testing.T) {
	schema := searchInputSchema()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("expected required []string, got %T", schema["required"])
	}
	found := false
	for _, r := range required {
		if r == "query" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected query to be required, got %v", required)
	}
}

// TestToolDescriptionsMentionBackendAndStrategy is the textual guard:
// every search tool description must tell the agent which backend it
// talks to (or, for honest unavailable tools, that it does NOT talk
// to a real provider). Agents that grep descriptions should never
// have to read source code to decide which tool to call.
func TestToolDescriptionsMentionBackendAndStrategy(t *testing.T) {
	s := &Server{}
	s.buildToolsRegistry()
	s.buildResourcesRegistry()

	// (tool, required substrings in description, lower-case)
	want := map[string][]string{
		"search_web":          {"searxng", "strategy"},
		"search_news":         {"searxng"},
		"search_doc_oficial":  {"official_doc_web_fallback"},
		"search_local_index":  {"local_index_unavailable"},
		"search_reddit":       {"searxng", "searxng_reddit_index"},
		"search_github":       {"github"},
		"search_github_pr":    {"github", "filters.state"},
		"search_github_issue": {"github", "filters.state"},
		"fetch_and_extract":   {"auto", "article", "documentation", "raw"},
		"summarize_results":   {"summary", "keyFindings"},
		"deep_research":       {"themes"},
		"compare_sources":     {"consensus", "divergences"},
		"get_current_date":    {"utc"},
	}

	for _, tool := range s.toolsRegistry {
		reqs, ok := want[tool.Name]
		if !ok {
			continue
		}
		lower := strings.ToLower(tool.Description)
		for _, r := range reqs {
			if !strings.Contains(lower, strings.ToLower(r)) {
				t.Errorf("tool %q description must mention %q so agents can decide; got %q", tool.Name, r, tool.Description)
			}
		}
	}
}

// TestSearchLocalIndexDescriptionAdvertisesUnavailability is the
// textual complement to TestSearchLocalIndexUnavailableSignal in
// contract_test.go: the description alone (without invoking the tool)
// must already tell the agent that the tool is unavailable today.
func TestSearchLocalIndexDescriptionAdvertisesUnavailability(t *testing.T) {
	s := &Server{}
	s.buildToolsRegistry()
	for _, tool := range s.toolsRegistry {
		if tool.Name != "search_local_index" {
			continue
		}
		lower := strings.ToLower(tool.Description)
		if !strings.Contains(lower, "local_index_unavailable") {
			t.Errorf("search_local_index description must advertise local_index_unavailable; got %q", tool.Description)
		}
		if !strings.Contains(lower, "no redirige") {
			t.Errorf("search_local_index description must tell agents it does not route to web; got %q", tool.Description)
		}
	}
}

// TestSearchDocOficialDescriptionAdvertisesFallback is the textual
// complement to TestSearchDocOficialStrategySignal in
// contract_test.go: the description alone (without invoking the tool)
// must already tell the agent that the tool falls back to web search.
func TestSearchDocOficialDescriptionAdvertisesFallback(t *testing.T) {
	s := &Server{}
	s.buildToolsRegistry()
	for _, tool := range s.toolsRegistry {
		if tool.Name != "search_doc_oficial" {
			continue
		}
		lower := strings.ToLower(tool.Description)
		if !strings.Contains(lower, "official_doc_web_fallback") {
			t.Errorf("search_doc_oficial description must advertise official_doc_web_fallback; got %q", tool.Description)
		}
		if !strings.Contains(lower, "fallback") {
			t.Errorf("search_doc_oficial description must mention the word 'fallback'; got %q", tool.Description)
		}
	}
}

// TestResourcesAdvertisedThroughToolsRegistryIsNotDuplicated makes
// sure the resources registry is not accidentally confused with the
// tools registry. The MCP spec exposes them under separate methods
// (tools/list vs resources/list), so the server must never leak
// resources into tools/list.
func TestResourcesAdvertisedThroughToolsRegistryIsNotDuplicated(t *testing.T) {
	s := buildResourcesTestServer(t)

	for _, tool := range s.Tools() {
		if strings.Contains(tool.Name, "agent-guide") || strings.Contains(tool.Name, "resources") {
			t.Errorf("tools registry must not contain resource-like names; found %q", tool.Name)
		}
	}
	if len(s.Resources()) != 1 {
		t.Errorf("expected 1 resource in registry, got %d", len(s.Resources()))
	}
}

// TestResourcesAccessorExposesRegistry proves the Server exposes the
// resources registry through a stable accessor (Resources) for tests
// and any future in-process consumer. Without this, tests would be
// forced to call HandleResourcesList and round-trip through JSON,
// making the regression guards noisy.
func TestResourcesAccessorExposesRegistry(t *testing.T) {
	s := buildResourcesTestServer(t)
	resources := s.Resources()
	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].URI != AgentGuideURI {
		t.Errorf("expected URI %q, got %q", AgentGuideURI, resources[0].URI)
	}
}

// TestSchemasRoundTripAsValidJSON is a defensive check: every schema
// the server advertises must serialize and deserialize as JSON without
// losing information. A future contributor adding a non-JSON-encodable
// type (e.g. a func) would break this test before they ship.
func TestSchemasRoundTripAsValidJSON(t *testing.T) {
	schemas := map[string]map[string]interface{}{
		"search":           searchInputSchema(),
		"fetchURL":         fetchURLInputSchema(),
		"fetchAndExtract":  fetchAndExtractInputSchema(),
		"url":              urlInputSchema(),
		"urlList":          urlListInputSchema(),
		"synthesis":        synthesisInputSchema(),
		"githubFilters":    githubFiltersInputSchema(),
		"empty":            emptyInputSchema(),
		"searchResultItem": searchResultItemSchema(),
	}
	for name, schema := range schemas {
		t.Run(name, func(t *testing.T) {
			data, err := json.Marshal(schema)
			if err != nil {
				t.Fatalf("marshal %s schema: %v", name, err)
			}
			var back map[string]interface{}
			if err := json.Unmarshal(data, &back); err != nil {
				t.Fatalf("unmarshal %s schema: %v", name, err)
			}
			if back["type"] != schema["type"] {
				t.Errorf("%s schema lost type after round-trip: got %v, want %v", name, back["type"], schema["type"])
			}
		})
	}
}
