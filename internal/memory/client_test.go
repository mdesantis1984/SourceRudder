package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestMemoryClientSaveEmptyBaseURLAvoidsIO is the RED gate for the
// pre-I/O short-circuit. When the client is constructed with an empty
// baseURL (the IA_Recuerdo integration is optional), Save MUST return
// nil immediately and MUST NOT issue any HTTP request. This is the
// shape of the integration that lets operators run the server without
// an external memory service.
func TestMemoryClientSaveEmptyBaseURLAvoidsIO(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := NewClient("", "") // empty baseURL: integration disabled
	payload := map[string]interface{}{"observation": "no http"}
	if err := c.Save(context.Background(), payload); err != nil {
		t.Fatalf("expected nil error for empty baseURL, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("expected zero HTTP requests when baseURL is empty, got %d", got)
	}
}

// TestMemoryClientSaveIssuesHTTPRequest is the RED gate for the
// non-empty path. The client MUST perform exactly one HTTP request
// against the configured endpoint and decode the response, returning
// any transport error from the call. A second call still issues
// exactly one request and surfaces the failure when the upstream
// answers with 500.
func TestMemoryClientSaveIssuesHTTPRequest(t *testing.T) {
	var hits int32
	var lastMethod string
	var lastPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		lastMethod = r.Method
		lastPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/v1/observations", "apikey-test-123")
	payload := map[string]interface{}{"observation": "hello"}
	if err := c.Save(context.Background(), payload); err != nil {
		t.Fatalf("expected nil error on 200 response, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected exactly 1 HTTP request, got %d", got)
	}
	if lastMethod != http.MethodPost {
		t.Fatalf("expected POST, got %s", lastMethod)
	}
	if lastPath != "/v1/observations" {
		t.Fatalf("expected path /v1/observations, got %s", lastPath)
	}
}

// TestMemoryClientSaveJSONEncodesPayload locks in that the client
// serializes the payload as a JSON body and sends the api key as a
// header (Authorization: Bearer ...). Triangulates against the empty
// path so a regression that hard-codes an empty body fails the test.
func TestMemoryClientSaveJSONEncodesPayload(t *testing.T) {
	var hits int32
	var lastAuthHeader string
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		lastAuthHeader = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		lastBody = buf
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/obs", "secret-key")
	payload := map[string]interface{}{"k": "v", "n": 7}
	if err := c.Save(context.Background(), payload); err != nil {
		t.Fatalf("expected nil error on 204 response, got %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected exactly 1 HTTP request, got %d", got)
	}
	if lastAuthHeader != "Bearer secret-key" {
		t.Fatalf("expected Authorization: Bearer secret-key, got %q", lastAuthHeader)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(lastBody, &decoded); err != nil {
		t.Fatalf("expected JSON body, decode failed: %v (body=%q)", err, string(lastBody))
	}
	if decoded["k"] != "v" || decoded["n"] != float64(7) {
		t.Fatalf("expected payload k=v n=7, got %#v", decoded)
	}
}

// TestMemoryClientSaveTransportFailureReturnsError verifies that when
// the configured baseURL points at a closed port, Save returns a
// non-nil error and does NOT panic. Triangulates the error path so a
// future regression that swallows transport errors surfaces here.
func TestMemoryClientSaveTransportFailureReturnsError(t *testing.T) {
	c := NewClient("http://127.0.0.1:1/obs", "k") // port 1: nothing listening
	err := c.Save(context.Background(), map[string]interface{}{"x": 1})
	if err == nil {
		t.Fatal("expected non-nil transport error, got nil")
	}
}
