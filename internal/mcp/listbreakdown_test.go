package mcp

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// TestToolsListByteBreakdown logs where the tools/list bytes go, so a later
// trim can be measured instead of guessed. It asserts nothing; the size
// guard lives in TestToolsListSizeStaysUnderTheTokenBudget.
func TestToolsListByteBreakdown(t *testing.T) {
	size := func(v interface{}) int {
		b, _ := json.Marshal(v)
		return len(b)
	}
	tools := buildTestServer(t).ListTools()
	parts := map[string]int{}
	propBytes := map[string]int{}
	repeated := map[string]int{}
	var walk func(v interface{}, prop string)
	walk = func(v interface{}, prop string) {
		m, ok := v.(map[string]interface{})
		if !ok {
			return
		}
		if props, ok := m["properties"].(map[string]interface{}); ok {
			for name, p := range props {
				if pm, ok := p.(map[string]interface{}); ok {
					if d, ok := pm["description"].(string); ok {
						propBytes[name] += len(d)
						repeated[d]++
					}
				}
				walk(p, name)
			}
		}
		if items, ok := m["items"]; ok {
			walk(items, prop)
		}
	}
	for _, tool := range tools {
		d, _ := tool["description"].(string)
		parts["tool descriptions"] += len(d)
		repeated[d]++
		parts["titles"] += size(tool["title"])
		parts["annotations"] += size(tool["annotations"])
		parts["outputSchema"] += size(tool["outputSchema"])
		parts["inputSchema"] += size(tool["inputSchema"])
		walk(tool["inputSchema"], "")
	}
	parts["total"] = size(map[string]interface{}{"tools": tools})
	for k, v := range parts {
		t.Logf("part %-18s %6d B", k, v)
	}
	for k, v := range propBytes {
		t.Logf("inputSchema description bytes, property %-12s %6d B", k, v)
	}
	type rep struct {
		s string
		n int
	}
	var reps []rep
	for s, n := range repeated {
		if n > 1 {
			reps = append(reps, rep{s, n})
		}
	}
	sort.Slice(reps, func(i, j int) bool { return len(reps[i].s)*reps[i].n > len(reps[j].s)*reps[j].n })
	for i, r := range reps {
		if i == 10 {
			break
		}
		t.Logf("repeated x%d (%d B each): %s", r.n, len(r.s), strings.ReplaceAll(r.s, "\n", " "))
	}
}
