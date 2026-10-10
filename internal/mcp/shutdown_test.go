package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/auth"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
)

func startShutdownTestServer(t *testing.T, tools []Tool) (*Server, string) {
	t.Helper()
	srv := &Server{
		transport:     "http",
		httpAddr:      "127.0.0.1:0",
		met:           observability.New(),
		authValidator: auth.NewValidator("shutdown-test-key"),
		toolsRegistry: tools,
	}
	if err := srv.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Keep the original HTTP server for cleanup even if Stop times out.
	httpSrv := srv.httpSrv
	t.Cleanup(func() {
		if err := httpSrv.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return srv, "http://" + srv.httpLn.Addr().String() + "/mcp"
}

func TestHTTPShutdownEndsSSE(t *testing.T) {
	for _, direct := range []bool{false, true} {
		name := "Stop"
		if direct {
			name = "HTTPShutdown"
		}
		t.Run(name, func(t *testing.T) {
			srv, url := startShutdownTestServer(t, nil)
			client := &http.Client{Timeout: 8 * time.Second}
			req, err := http.NewRequest(http.MethodGet, url, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer shutdown-test-key")
			req.Header.Set("Mcp-Session-Id", "shutdown-session")
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Mcp-Session-Id") != "shutdown-session" {
				t.Fatalf("unexpected SSE response: status=%d headers=%v", resp.StatusCode, resp.Header)
			}
			reader := bufio.NewReader(resp.Body)
			if line, err := reader.ReadString('\n'); err != nil || line != ": ping\n" {
				t.Fatalf("initial ping: line=%q err=%v", line, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			if direct {
				err = srv.httpSrv.Shutdown(ctx)
			} else {
				err = srv.Stop(ctx)
			}
			duration := time.Since(start)
			t.Logf("shutdown duration: %v", duration)
			if err != nil {
				t.Fatalf("shutdown after %v: %v", duration, err)
			}
			if duration >= 2*time.Second {
				t.Fatalf("shutdown took %v, want < 2s", duration)
			}
			if remaining, err := io.ReadAll(reader); err != nil || string(remaining) != "\n" {
				t.Fatalf("stream did not end cleanly: remaining=%q err=%v", remaining, err)
			}
			if err := srv.Stop(ctx); err != nil {
				t.Fatalf("second Stop: %v", err)
			}
		})
	}
}

func TestHTTPStopWaitsForInFlightPOST(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	srv, url := startShutdownTestServer(t, []Tool{{
		Name: "blocking_tool",
		Handler: func(ctx context.Context, _ json.RawMessage) (interface{}, error) {
			close(entered)
			select {
			case <-release:
				return "completed", ctx.Err()
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
	}})
	defer unblock()
	postDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 8 * time.Second}
		req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"blocking_tool","arguments":{}}}`))
		if err != nil {
			postDone <- err
			return
		}
		req.Header.Set("Authorization", "Bearer shutdown-test-key")
		resp, err := client.Do(req)
		if err != nil {
			postDone <- err
			return
		}
		defer resp.Body.Close()
		var result struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		err = json.NewDecoder(resp.Body).Decode(&result)
		if err == nil && (resp.StatusCode != http.StatusOK || len(result.Error) != 0 || !strings.Contains(string(result.Result), "completed")) {
			err = io.ErrUnexpectedEOF
		}
		postDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("POST did not enter tool handler")
	}
	shutdownStarted := make(chan struct{})
	srv.httpSrv.RegisterOnShutdown(func() { close(shutdownStarted) })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- srv.Stop(ctx) }()
	select {
	case <-shutdownStarted:
	case <-ctx.Done():
		t.Fatal("shutdown did not begin")
	}
	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned before POST completed: %v", err)
	case err := <-postDone:
		t.Fatalf("POST ended before release: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	unblock()
	if err := <-postDone; err != nil {
		t.Fatalf("POST: %v", err)
	}
	if err := <-stopDone; err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestStopWithoutStart(t *testing.T) {
	for _, transport := range []string{"", "http", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			srv := &Server{transport: transport}
			for i := 0; i < 2; i++ {
				if err := srv.Stop(context.Background()); err != nil {
					t.Fatalf("Stop %d: %v", i+1, err)
				}
			}
		})
	}
}
