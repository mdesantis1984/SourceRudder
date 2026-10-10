package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProtocolVersionNegotiation(t *testing.T) {
	s := buildResourcesTestServer(t)
	cases := []struct {
		name, params, want string
	}{
		{"latest", `{"protocolVersion":"2025-11-25"}`, "2025-11-25"},
		{"june", `{"protocolVersion":"2025-06-18"}`, "2025-06-18"},
		{"legacy", `{"protocolVersion":"2024-11-05"}`, "2024-11-05"},
		{"batch_version", `{"protocolVersion":"2025-03-26"}`, "2025-11-25"},
		{"unknown", `{"protocolVersion":"2099-01-01"}`, "2025-11-25"},
		{"empty", `{"protocolVersion":""}`, "2025-11-25"},
		{"missing", `{}`, "2025-11-25"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("direct", func(t *testing.T) {
				result, err := s.HandleInitialize(context.Background(), json.RawMessage(tc.params))
				if err != nil {
					t.Fatal(err)
				}
				m := result.(map[string]interface{})
				if got := m["protocolVersion"]; got != tc.want {
					t.Errorf("protocolVersion = %v; want %s", got, tc.want)
				}
				if m["sessionId"] == "" || m["sessionId"] == nil {
					t.Error("missing sessionId")
				}
			})
			t.Run("http", func(t *testing.T) {
				body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` + tc.params + `}`
				w := httptest.NewRecorder()
				s.HandleHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body)))
				if w.Code != http.StatusOK {
					t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
				}
				var response struct {
					Result struct {
						ProtocolVersion string `json:"protocolVersion"`
					} `json:"result"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if got := response.Result.ProtocolVersion; got != tc.want {
					t.Errorf("protocolVersion = %q; want %q", got, tc.want)
				}
			})
		})
	}
	// A truly absent params field also negotiates the latest version on the wire.
	w := httptest.NewRecorder()
	s.HandleHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)))
	if !strings.Contains(w.Body.String(), `"protocolVersion":"2025-11-25"`) {
		t.Errorf("missing params: %s", w.Body.String())
	}
}

func TestProtocolVersionHTTPHeader(t *testing.T) {
	s := buildResourcesTestServer(t)
	cases := []struct {
		name, version string
		present       bool
		want          int
	}{
		{"latest", "2025-11-25", true, http.StatusOK},
		{"june", "2025-06-18", true, http.StatusOK},
		{"legacy", "2024-11-05", true, http.StatusOK},
		{"batch_version", "2025-03-26", true, http.StatusBadRequest},
		{"unknown", "2099-01-01", true, http.StatusBadRequest},
		{"empty", "", true, http.StatusBadRequest},
		{"absent", "", false, http.StatusOK},
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, tc := range cases {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(method, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
				// Cancel GET's streaming context so successful requests terminate immediately.
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
				if tc.present {
					req.Header.Set("MCP-Protocol-Version", tc.version)
				}
				w := httptest.NewRecorder()
				s.HandleHTTP(w, req)
				if w.Code != tc.want {
					t.Fatalf("status = %d; want %d; body = %s", w.Code, tc.want, w.Body.String())
				}
				if tc.want == http.StatusBadRequest {
					var response struct {
						JSONRPC string `json:"jsonrpc"`
						Error   struct {
							Code    int    `json:"code"`
							Message string `json:"message"`
						} `json:"error"`
					}
					if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
						t.Fatal(err)
					}
					if response.JSONRPC != "2.0" || response.Error.Code != -32600 || !strings.Contains(response.Error.Message, "unsupported") || !strings.Contains(response.Error.Message, tc.version) {
						t.Errorf("invalid error response: %s", w.Body.String())
					}
				}
			})
		}
	}
}

func TestProtocolVersionOptionsAndCORS(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/mcp", nil)
	req.Header.Set("MCP-Protocol-Version", "unsupported")
	w := httptest.NewRecorder()
	buildResourcesTestServer(t).HandleHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d; want 204", w.Code)
	}
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Headers"), "MCP-Protocol-Version") {
		t.Errorf("protocol header missing from CORS: %s", w.Header().Get("Access-Control-Allow-Headers"))
	}
}

func TestProtocolVersionInvalidInitializeParams(t *testing.T) {
	s := buildResourcesTestServer(t)
	for _, params := range []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`{"protocolVersion":123}`)} {
		if _, err := s.HandleInitialize(context.Background(), params); err == nil || !strings.Contains(err.Error(), "invalid params") {
			t.Errorf("params %q: want invalid params error, got %v", params, err)
		}
	}
}
