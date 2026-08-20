package main

import (
	"context"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/mcp"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

func TestServerStart(t *testing.T) {
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	fetchSvc := fetch.NewFetcherService(5000)
	synthSvc := synthesis.NewService()
	server := mcp.NewServer(cm, planner, "stdio", ":8080", "http://localhost:8888", 300, 5000, fetchSvc, synthSvc, nil, observability.New())
	if server == nil {
		t.Fatal("expected non-nil server")
	}
}

func TestToolsCount(t *testing.T) {
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	fetchSvc := fetch.NewFetcherService(5000)
	synthSvc := synthesis.NewService()
	server := mcp.NewServer(cm, planner, "stdio", ":8080", "http://localhost:8888", 300, 5000, fetchSvc, synthSvc, nil, observability.New())
	tools := server.Tools()
	if len(tools) != 25 {
		t.Errorf("expected 25 tools, got %d", len(tools))
	}
}

func TestHandleInitialize(t *testing.T) {
	cacheSvc := cache.NewService(300)
	cm := search.NewConnectorManager(cacheSvc)
	planner := search.NewPlanner()
	fetchSvc := fetch.NewFetcherService(5000)
	synthSvc := synthesis.NewService()
	server := mcp.NewServer(cm, planner, "stdio", ":8080", "http://localhost:8888", 300, 5000, fetchSvc, synthSvc, nil, observability.New())
	result, err := server.HandleInitialize(context.Background(), []byte(`{"clientId": "test"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]interface{})
	if m["serverInfo"] == nil {
		t.Error("expected serverInfo in result")
	}
}
