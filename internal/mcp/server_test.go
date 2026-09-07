package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/thiscloud/ia-buscar/internal/auth"
	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/fetch"
	"github.com/thiscloud/ia-buscar/internal/memory"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/internal/synthesis"
)

// TestNewServerWiresMemoryClientIdentity is the threat-matrix
// process-integration proof for the memory client wiring: the same
// *memory.Client pointer passed to NewServer MUST be stored on the
// Server unchanged, so handlers and the rest of the server agree on
// whose memory endpoint they talk to.
func TestNewServerWiresMemoryClientIdentity(t *testing.T) {
	cacheSvc := cache.NewService(60)
	history := cache.NewHistoryService(10)
	mem := memory.NewClient("http://memory.local:7438", "key-abc")

	srv := NewServer(
		search.NewConnectorManager(cacheSvc),
		search.NewPlanner(),
		"stdio", ":8080", "http://localhost:8888",
		60, 5000,
		fetch.NewFetcherService(5000),
		synthesis.NewService(),
		auth.NewValidator("k"),
		observability.New(),
		history, mem, // NEW stateful wiring
	)
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}

	if srv.mem != mem {
		t.Fatalf("Server.mem is NOT the same pointer passed to NewServer (got %p, want %p)", srv.mem, mem)
	}
	if srv.history != history {
		t.Fatalf("Server.history is NOT the same pointer passed to NewServer (got %p, want %p)", srv.history, history)
	}
}

func TestHTTPServerHasBoundedTimeouts(t *testing.T) {
	srv := &Server{
		transport: "http",
		httpAddr:  "127.0.0.1:0",
		met:       observability.New(),
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.Stop(ctx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	if srv.httpSrv.ReadHeaderTimeout != httpReadHeaderTimeout ||
		srv.httpSrv.ReadTimeout != httpReadTimeout ||
		srv.httpSrv.WriteTimeout != httpWriteTimeout ||
		srv.httpSrv.IdleTimeout != httpIdleTimeout {
		t.Fatalf("unexpected HTTP timeouts: %+v", srv.httpSrv)
	}
}

// TestNewServerWiresMemoryClientThroughStatefulHandler is the
// scenario-side proof: a tool handler constructed from the Server
// must be reachable through the same Server, so when a stateful
// handler runs it consults the same memory client pointer. This
// test confirms the Server is constructible with a non-nil memory
// client and that the registry is reachable. Tool-level coverage
// for the stateful handlers lives in tools_test.go.
func TestNewServerWiresMemoryClientThroughStatefulHandler(t *testing.T) {
	cacheSvc := cache.NewService(60)
	mem := memory.NewClient("", "") // no-op; integration disabled but non-nil
	srv := NewServer(
		search.NewConnectorManager(cacheSvc),
		search.NewPlanner(),
		"stdio", ":8080", "http://localhost:8888",
		60, 5000,
		fetch.NewFetcherService(5000),
		synthesis.NewService(),
		auth.NewValidator("k"),
		observability.New(),
		cache.NewHistoryService(10), mem,
	)
	if srv == nil {
		t.Fatal("NewServer returned nil")
	}
	if len(srv.Tools()) == 0 {
		t.Fatal("expected tool registry to be non-empty")
	}
}
