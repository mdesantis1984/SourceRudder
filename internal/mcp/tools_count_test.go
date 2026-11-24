package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// TestToolCountMatchesStaticGrep is a sanity bridge between the
// runtime registry and the source-of-truth grep guard. The grep guard
// `grep -cE 'Name:\\s+\"' internal/mcp/server.go` MUST equal 30.
// The test encodes the contract directly so a future operator that
// forgets to re-run the grep still gets the assertion through the
// test suite.
func TestToolCountMatchesStaticGrep(t *testing.T) {
	s := buildTestServer(t)
	if got := len(s.Tools()); got != 30 {
		t.Fatalf("registry holds %d tools; expected 30 (grep guard is the source of truth)", got)
	}
	// Round-trip through the public ListTools path so the wire shape
	// (which strips handler funcs) is asserted instead of the
	// internal struct shape.
	data, err := json.Marshal(s.ListTools())
	if err != nil {
		t.Fatalf("Marshal registry: %v", err)
	}
	var roundTripped []map[string]interface{}
	if err := json.Unmarshal(data, &roundTripped); err != nil {
		t.Fatalf("Unmarshal registry: %v", err)
	}
	if len(roundTripped) != 30 {
		t.Fatalf("wire-roundtrip count: got %d, want 30", len(roundTripped))
	}
}

// TestHandleToolsListWireShape asserts the JSON-RPC shape returned by
// tools/list. The shape MUST be
// {"tools":[{"name":..., "description":..., "inputSchema":...}, ...]}
// with every entry carrying a non-empty string name + description.
func TestHandleToolsListWireShape(t *testing.T) {
	s := buildTestServer(t)

	res, err := s.HandleToolsList(context.Background(), nil)
	if err != nil {
		t.Fatalf("HandleToolsList error: %v", err)
	}
	m := res.(map[string]interface{})
	toolsList, ok := m["tools"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected []map[string]interface{}, got %T", m["tools"])
	}
	if len(toolsList) != 30 {
		t.Fatalf("expected 30 tools, got %d", len(toolsList))
	}
	for _, entry := range toolsList {
		name, _ := entry["name"].(string)
		desc, _ := entry["description"].(string)
		if name == "" {
			t.Errorf("tool entry has empty name: %#v", entry)
		}
		if desc == "" {
			t.Errorf("tool %q has empty description", name)
		}
	}
}
