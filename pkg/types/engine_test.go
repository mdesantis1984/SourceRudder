package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSearchResultEngineOptional(t *testing.T) {
	result := SearchResultItem{Title: "result", Source: "searxng"}
	wire, err := json.Marshal(result)
	if err != nil || strings.Contains(string(wire), `"engine"`) {
		t.Fatalf("absent engine must be omitted: %s err=%v", wire, err)
	}
	if err := json.Unmarshal([]byte(`{"title":"legacy","source":"searxng"}`), &result); err != nil || result.Engine != "" {
		t.Fatalf("legacy result must decode: %+v err=%v", result, err)
	}
}
