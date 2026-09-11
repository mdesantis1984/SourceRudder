package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/auth"
	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/fetch"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/internal/synthesis"
)

type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadline time.Time
}

func (w *deadlineRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *deadlineRecorder) Flush() {}

func TestNewServerWiresHistoryServiceIdentity(t *testing.T) {
	cacheSvc := cache.NewService(60)
	history := cache.NewHistoryService(10)

	srv := NewServer(
		search.NewConnectorManager(cacheSvc),
		search.NewPlanner(),
		"stdio", ":8080", "http://localhost:8888",
		60, 5000,
		fetch.NewFetcherService(5000),
		synthesis.NewService(),
		auth.NewValidator("k"),
		observability.New(),
		history,
	)
	if srv == nil {
		t.Fatal("NewServer returned nil")
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

func TestHTTPRejectsOversizedRPCRequest(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(strings.Repeat(" ", maxRPCRequestBytes+1)))
	rec := httptest.NewRecorder()
	srv.handleHTTPPost(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status: got %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestSSEBoundsHeartbeatWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	(&Server{}).handleHTTPGet(rec, httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx))
	if !rec.deadline.After(time.Now()) || rec.deadline.After(time.Now().Add(6*time.Second)) {
		t.Fatalf("SSE heartbeat write deadline is not bounded: %v", rec.deadline)
	}
}
