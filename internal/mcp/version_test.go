package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
)

// TestServerIdentityIsSourceRudder3 verifies that both initialize paths
// advertise the same major-version product identity.
func TestServerIdentityIsSourceRudder3(t *testing.T) {
	cacheSvc := cache.NewService(60)
	cm := search.NewConnectorManager(cacheSvc)
	srv := NewServer(cm, search.NewPlanner(), "stdio", ":8080", "http://localhost:8888", 60, 5000, nil, synthesis.NewService(), nil, observability.New(), cache.NewHistoryService(10))

	params, _ := json.Marshal(map[string]interface{}{})
	resp, err := srv.HandleInitialize(context.Background(), params)
	if err != nil {
		t.Fatalf("HandleInitialize: %v", err)
	}
	gotName, gotVersion := identityFromInitialize(resp)
	if gotName != "sourcerudder" || gotVersion != "3.0.0" {
		t.Fatalf("HandleInitialize reports %q %q; want sourcerudder 3.0.0", gotName, gotVersion)
	}

	httpInit := srv.handleMCPInitialize(1)
	gotHTTPName, gotHTTPVersion := identityFromInitialize(httpInit["result"])
	if gotHTTPName != "sourcerudder" || gotHTTPVersion != "3.0.0" {
		t.Fatalf("handleMCPInitialize reports %q %q; want sourcerudder 3.0.0", gotHTTPName, gotHTTPVersion)
	}
}

func identityFromInitialize(resp interface{}) (string, string) {
	m, ok := resp.(map[string]interface{})
	if !ok {
		return "<not-a-map>", "<not-a-map>"
	}
	si, ok := m["serverInfo"].(map[string]interface{})
	if !ok {
		return "<no-serverInfo>", "<no-serverInfo>"
	}
	name, _ := si["name"].(string)
	version, _ := si["version"].(string)
	return name, version
}
