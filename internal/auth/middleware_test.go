package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestValidator_NeverNilOnEmptyKey is the Phase 12.1 RED gate. The
// previous Validator returned nil when the operator did not pass an
// API key, and the middleware used that as a bypass — every request
// skipped credential checking entirely. After Phase 12.2 the empty
// key path MUST produce a rejecting Validator so the middleware
// enforces 401 instead of silently opening the door.
func TestValidator_NeverNilOnEmptyKey(t *testing.T) {
	v := NewValidator("")
	if v == nil {
		t.Fatal("NewValidator(\"\") returned nil; expected a rejecting validator (the middleware relies on a non-nil receiver to enforce auth)")
	}
}

// TestValidator_RejectsMissingCredentials is the Phase 12.1 RED gate.
// A request without X-Api-Key and without Authorization: Bearer MUST
// be answered with 401 even when the operator did not configure any
// key (the empty-key validator actively rejects). The previous
// implementation skipped credential checks entirely.
func TestValidator_RejectsMissingCredentials(t *testing.T) {
	cases := []struct {
		name    string
		apiKey  string
		headers map[string]string
	}{
		{name: "empty-key-no-headers", apiKey: "", headers: nil},
		{name: "empty-key-bearer-empty", apiKey: "", headers: map[string]string{"Authorization": "Bearer "}},
		{name: "empty-key-x-api-key-empty", apiKey: "", headers: map[string]string{"X-Api-Key": ""}},
		{name: "key-set-no-headers", apiKey: "secret-key", headers: nil},
		{name: "key-set-empty-bearer", apiKey: "secret-key", headers: map[string]string{"Authorization": "Bearer "}},
		{name: "key-set-empty-x-api-key", apiKey: "secret-key", headers: map[string]string{"X-Api-Key": ""}},
		{name: "key-set-unrelated-header", apiKey: "secret-key", headers: map[string]string{"X-Other": "x"}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			v := NewValidator(tt.apiKey)
			if v == nil {
				t.Fatalf("NewValidator(%q) returned nil; refusing to test middleware", tt.apiKey)
			}
			called := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodGet, "http://example.com/mcp", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			v.Middleware(next).ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d (handler called=%v)", rec.Code, called)
			}
			if called {
				t.Fatal("next handler was invoked despite missing credentials; this is the exact bypass the gate forbids")
			}
		})
	}
}

// TestValidator_RejectsInvalidCredentials locks in the contract that
// a wrong X-Api-Key or Bearer MUST NOT reach the downstream handler.
// A constant-time comparison prevents invalid credentials from reaching
// the downstream handler.
func TestValidator_RejectsInvalidCredentials(t *testing.T) {
	v := NewValidator("correct-key")
	if v == nil {
		t.Fatal("NewValidator returned nil")
	}
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "http://example.com/mcp", nil)
	req.Header.Set("X-Api-Key", "wrong-key")
	rec := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong X-Api-Key: expected 401, got %d", rec.Code)
	}
	if called {
		t.Fatal("next handler invoked with wrong X-Api-Key")
	}

	// Same via Authorization.
	req2 := httptest.NewRequest(http.MethodGet, "http://example.com/mcp", nil)
	req2.Header.Set("Authorization", "Bearer wrong-key")
	rec2 := httptest.NewRecorder()
	v.Middleware(next).ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("wrong Bearer: expected 401, got %d", rec2.Code)
	}
}

// TestValidator_AcceptsMatchingCredentials is the GREEN happy path.
// Once a key is configured, the matching key (in either header) MUST
// reach the downstream handler. Exact equality is the contract; the
// test exercises both header surfaces so a future refactor that
// drops one will fail loudly.
func TestValidator_AcceptsMatchingCredentials(t *testing.T) {
	v := NewValidator("correct-key")
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	for _, hdr := range []struct{ name, value string }{
		{"X-Api-Key", "correct-key"},
		{"Authorization", "Bearer correct-key"},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/mcp", nil)
		req.Header.Set(hdr.name, hdr.value)
		rec := httptest.NewRecorder()
		v.Middleware(next).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d", hdr.name, rec.Code)
		}
		if !called {
			t.Fatalf("%s: next handler never invoked", hdr.name)
		}
		called = false
	}
}
