package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/memory"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

// TestServerVersionIs_1_4_0 verifies that the MCP server advertises version
// "1.4.0" in BOTH initialize paths (the JSON-RPC
// HTTP handleMCPInitialize and the typed HandleInitialize). A bump in
// one path but not the other would silently desync MCP clients.
func TestServerVersionIs_1_4_0(t *testing.T) {
	cacheSvc := cache.NewService(60)
	cm := search.NewConnectorManager(cacheSvc)
	srv := NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 60, 5000, nil, synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10), memory.NewClient("", ""))

	params, _ := json.Marshal(map[string]interface{}{})
	resp, err := srv.HandleInitialize(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleInitialize: %v", err)
	}
	got := versionFromInitialize(resp)
	if got != "1.4.0" {
		t.Fatalf("HandleInitialize reports version %q; want \"1.4.0\"", got)
	}

	httpInit := srv.handleMCPInitialize(1)
	gotHTTP := versionFromInitialize(httpInit["result"])
	if gotHTTP != "1.4.0" {
		t.Fatalf("handleMCPInitialize reports version %q; want \"1.4.0\"", gotHTTP)
	}
}

// versionFromInitialize digs into the nested map[string]interface{}
// shape the server emits for "initialize" responses and returns the
// serverInfo.version string. A wrong shape fails the test loudly so
// future refactors that change the wire contract get a clear signal.
func versionFromInitialize(resp interface{}) string {
	m, ok := resp.(map[string]interface{})
	if !ok {
		return "<not-a-map>"
	}
	si, ok := m["serverInfo"].(map[string]interface{})
	if !ok {
		return "<no-serverInfo>"
	}
	v, ok := si["version"].(string)
	if !ok {
		return "<no-version-string>"
	}
	return v
}
