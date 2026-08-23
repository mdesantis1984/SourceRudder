package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/thiscloud/ia-buscar/pkg/types"
)

func TestStubToolsRegistered(t *testing.T) {
	s := &Server{
		toolsRegistry: []Tool{},
	}
	s.buildToolsRegistry()
	if len(s.toolsRegistry) != 28 {
		t.Errorf("expected 28 tools (restored runtime contract), got %d", len(s.toolsRegistry))
	}
}

func TestStubSearchHandler(t *testing.T) {
	s := &Server{}
	args := json.RawMessage(`{"query": "test query"}`)
	result, err := s.stubSearchHandler(context.Background(), args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp, ok := result.(*types.SearchResponse)
	if !ok {
		t.Fatalf("expected *types.SearchResponse, got %T", result)
	}
	if resp.Query != "test query" {
		t.Errorf("expected query 'test query', got %s", resp.Query)
	}
}

func TestGetCurrentDateHandler(t *testing.T) {
	s := &Server{}
	result, err := s.getCurrentDateHandler(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	dateMap := result.(map[string]interface{})
	if dateMap["date"] == "" {
		t.Error("expected date in result")
	}
}
