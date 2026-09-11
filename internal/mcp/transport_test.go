package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/memory"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
)

func TestSTDIOTransportProcessesRequestsAndNotifications(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
	}, "\n") + "\n"
	var output bytes.Buffer
	transport := &STDIOTransport{
		server: newSTDIOTestServer(),
		input:  strings.NewReader(input),
		output: &output,
	}

	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("response lines = %d, want 2: %q", len(lines), output.String())
	}
	var initialize map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &initialize); err != nil {
		t.Fatalf("decode initialize response: %v", err)
	}
	if initialize["id"] != float64(1) {
		t.Fatalf("initialize id = %v, want 1", initialize["id"])
	}
	var tools map[string]interface{}
	if err := json.Unmarshal([]byte(lines[1]), &tools); err != nil {
		t.Fatalf("decode tools response: %v", err)
	}
	result := tools["result"].(map[string]interface{})
	if got := len(result["tools"].([]interface{})); got != 28 {
		t.Fatalf("tools count = %d, want 28", got)
	}
}

func TestSTDIOTransportProcessesBatch(t *testing.T) {
	input := `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":2,"method":"tools/list"}]` + "\n"
	var output bytes.Buffer
	transport := &STDIOTransport{
		server: newSTDIOTestServer(),
		input:  strings.NewReader(input),
		output: &output,
	}

	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	var responses []map[string]interface{}
	if err := json.Unmarshal(output.Bytes(), &responses); err != nil {
		t.Fatalf("decode batch response: %v", err)
	}
	if len(responses) != 2 {
		t.Fatalf("batch responses = %d, want 2", len(responses))
	}
}

func TestSTDIOTransportReturnsParseError(t *testing.T) {
	var output bytes.Buffer
	transport := &STDIOTransport{
		server: newSTDIOTestServer(),
		input:  strings.NewReader("not-json\n"),
		output: &output,
	}

	if err := transport.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	var response struct {
		Error struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatalf("decode parse error: %v", err)
	}
	if response.Error.Code != -32700 {
		t.Fatalf("parse error code = %d, want -32700", response.Error.Code)
	}
}

func newSTDIOTestServer() *Server {
	cacheService := cache.NewService(300)
	return NewServer(
		search.NewConnectorManager(cacheService),
		search.NewPlanner(),
		"stdio",
		":8080",
		"http://localhost:8888",
		300,
		5000,
		fetch.NewFetcherService(5000),
		synthesis.NewService(),
		nil,
		observability.New(),
		cache.NewHistoryService(10),
		memory.NewClient("", ""),
	)
}
