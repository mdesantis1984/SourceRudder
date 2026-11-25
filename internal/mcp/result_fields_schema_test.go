package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSearchOutputSchemaDeclaresDateEngineAndAuthor pins the optional result
// fields that codemode and other TypeScript clients type from outputSchema:
// news callers need the date, video callers the channel, and every caller the
// engine that produced the item.
func TestSearchOutputSchemaDeclaresDateEngineAndAuthor(t *testing.T) {
	s := buildTestServer(t)
	data, err := json.Marshal(s.ListTools())
	if err != nil {
		t.Fatalf("Marshal tools: %v", err)
	}
	var tools []struct {
		Name         string `json:"name"`
		OutputSchema struct {
			Properties struct {
				Results struct {
					Items struct {
						Required   []string                          `json:"required"`
						Properties map[string]map[string]interface{} `json:"properties"`
					} `json:"items"`
				} `json:"results"`
			} `json:"properties"`
		} `json:"outputSchema"`
	}
	if err := json.Unmarshal(data, &tools); err != nil {
		t.Fatalf("Unmarshal tools: %v", err)
	}
	want := map[string]map[string]interface{}{
		"publishedAt": {"type": "string", "format": "date-time"},
		"engine":      {"type": "string"},
		"author":      {"type": "string"},
	}
	checked := 0
	for _, tool := range tools {
		if !strings.HasPrefix(tool.Name, "search_") {
			continue
		}
		checked++
		item := tool.OutputSchema.Properties.Results.Items
		for field, schema := range want {
			got, ok := item.Properties[field]
			if !ok {
				t.Errorf("%s: result item does not declare %q", tool.Name, field)
				continue
			}
			for k, v := range schema {
				if got[k] != v {
					t.Errorf("%s: %s.%s = %v, want %v", tool.Name, field, k, got[k], v)
				}
			}
		}
		for _, r := range item.Required {
			if _, optional := want[r]; optional {
				t.Errorf("%s: %q must stay optional", tool.Name, r)
			}
		}
	}
	if checked != 17 {
		t.Fatalf("checked %d search_* tools, want 17", checked)
	}
}
