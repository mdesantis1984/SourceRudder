package mcp

import "strings"

// Tool metadata advertised in tools/list (MCP 2025-11-25): a short human
// title, behavior annotations and, for the search and synthesis tools, an
// outputSchema their structuredContent conforms to. Clients treat the
// annotations as hints only.

// toolTitles maps each tool name to its short display title.
var toolTitles = map[string]string{
	"search_web":               "Web search",
	"search_news":              "News search",
	"search_doc_oficial":       "Official docs search",
	"search_local_index":       "Local index search",
	"search_github":            "GitHub search",
	"search_github_pr":         "GitHub PR search",
	"search_github_issue":      "GitHub issue search",
	"search_stackoverflow":     "StackOverflow search",
	"search_npm":               "npm search",
	"search_nuget":             "NuGet search",
	"search_pypi":              "PyPI search",
	"search_docker_hub":        "Docker Hub search",
	"search_academic":          "Academic search",
	"search_reddit":            "Reddit search",
	"search_youtube":           "YouTube search",
	"search_images":            "Image search",
	"fetch_url":                "Fetch URL",
	"fetch_and_extract":        "Fetch and extract",
	"extract_structured":       "Extract structured data",
	"validate_url":             "Validate URL",
	"check_link_status":        "Check link status",
	"summarize_results":        "Summarize results",
	"deep_research":            "Deep research synthesis",
	"compare_sources":          "Compare sources",
	"get_cached":               "Get cache entry",
	"invalidate_cache":         "Invalidate cache entry",
	"get_search_history":       "Search history",
	"fetch_youtube_transcript": "YouTube transcript",
	"get_current_date":         "Current date",
}

// closedWorldTools never reach beyond the process: synthesis works on the
// caller's data and the rest read or edit in-process state.
var closedWorldTools = map[string]bool{
	"search_local_index": true,
	"summarize_results":  true, "deep_research": true, "compare_sources": true,
	"get_cached": true, "invalidate_cache": true, "get_search_history": true, "get_current_date": true,
}

var synthesisToolNames = map[string]bool{"summarize_results": true, "deep_research": true, "compare_sources": true}

// toolAnnotations returns the MCP behavior hints of a tool. Every tool only
// reads, except invalidate_cache, which drops one cache entry (idempotent,
// and the entry is rebuilt on the next search).
func toolAnnotations(name string) map[string]interface{} {
	return map[string]interface{}{
		"readOnlyHint":    name != "invalidate_cache",
		"destructiveHint": false,
		"idempotentHint":  true,
		"openWorldHint":   !closedWorldTools[name],
	}
}

// decorateTools fills title, annotations and outputSchema on the registry.
func decorateTools(tools []Tool) {
	search, synthesis := searchOutputSchema(), synthesisOutputSchema()
	for i := range tools {
		t := &tools[i]
		t.Title = toolTitles[t.Name]
		t.Annotations = toolAnnotations(t.Name)
		switch {
		case strings.HasPrefix(t.Name, "search_"):
			t.OutputSchema = search
		case synthesisToolNames[t.Name]:
			t.OutputSchema = synthesis
		}
	}
}

// describeTool is the single tools/list entry shape, shared by every
// serialization site.
func describeTool(t Tool) map[string]interface{} {
	out := map[string]interface{}{
		"name":        t.Name,
		"description": t.Description,
		"inputSchema": t.InputSchema,
	}
	if t.Title != "" {
		out["title"] = t.Title
	}
	if t.Annotations != nil {
		out["annotations"] = t.Annotations
	}
	if t.OutputSchema != nil {
		out["outputSchema"] = t.OutputSchema
	}
	return out
}

func describeTools(tools []Tool) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		out = append(out, describeTool(t))
	}
	return out
}

func jsonType(t string, desc ...string) map[string]interface{} {
	m := map[string]interface{}{"type": t}
	if len(desc) > 0 {
		m["description"] = desc[0]
	}
	return m
}

func stringArray() map[string]interface{} {
	return map[string]interface{}{"type": "array", "items": jsonType("string")}
}

// searchOutputSchema is the lean structuredContent contract of every
// search_* tool (16 tools repeat it in tools/list, so it declares only what a
// caller or codemode script needs to type). The response carries more fields;
// additional properties are allowed.
func searchOutputSchema() map[string]interface{} {
	item := map[string]interface{}{
		"type":     "object",
		"required": []string{"title", "url", "source"},
		"properties": map[string]interface{}{
			"title":   jsonType("string"),
			"url":     jsonType("string"),
			"snippet": jsonType("string"),
			"source":  jsonType("string"),
		},
	}
	return map[string]interface{}{
		"type":     "object",
		"required": []string{"query", "results", "total"},
		"properties": map[string]interface{}{
			"query":       jsonType("string"),
			"results":     map[string]interface{}{"type": "array", "items": item},
			"resultSetId": jsonType("string"),
			"total":       jsonType("integer"),
			"nextCursor":  jsonType("string"),
			"partial":     jsonType("boolean"),
			"warnings":    stringArray(),
			"errors":      stringArray(),
		},
	}
}

// synthesisOutputSchema covers summarize_results, deep_research and
// compare_sources; each tool fills the subset its description lists.
func synthesisOutputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"summary":     jsonType("string"),
			"keyFindings": stringArray(),
			"citations":   stringArray(),
			"confidence":  jsonType("number"),
			"themes":      map[string]interface{}{"type": "array", "items": jsonType("object")},
			"comparison":  jsonType("object"),
			"sources":     map[string]interface{}{"type": "array", "items": jsonType("object")},
			"consensus":   jsonType("string"),
			"divergences": stringArray(),
			"query":       jsonType("string"),
			"warnings":    stringArray(),
		},
	}
}
