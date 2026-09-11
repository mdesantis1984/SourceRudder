// Package memory implements the IA_Recuerdo client used to ship
// observations from SourceRudder to the external memory service.
//
// The integration is optional: a Client built with an empty baseURL
// short-circuits every Save call to nil WITHOUT issuing any HTTP
// request. Operators who do not run IA_Recuerdo in their environment
// get a no-op client without paying a network round-trip on every
// observation. The MCP wiring uses the same pointer for every
// stateful tool, so the storage identity is preserved end-to-end.
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client is the IA_Recuerdo transport. It is safe for concurrent use
// by multiple goroutines because the underlying http.Client is
// goroutine-safe and the Save method only reads immutable fields.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient constructs a Client that POSTs observations to
// baseURL+endpoint. An empty baseURL produces a no-op client whose
// Save always returns nil without touching the network. The apiKey
// travels as `Authorization: Bearer <apiKey>`; an empty key sends no
// Authorization header so internal-only deployments still work.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Save ships one observation to the configured memory endpoint. It
// returns nil immediately when the integration is disabled (empty
// baseURL), so callers do not need to special-case the no-op path.
// When enabled, the payload is JSON-encoded into the request body and
// the configured apiKey travels as `Authorization: Bearer ...`. Any
// transport or non-2xx response surfaces as a non-nil error so the
// caller can decide whether to retry or skip.
func (c *Client) Save(ctx context.Context, payload interface{}) error {
	if c == nil || c.baseURL == "" {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("memory: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("memory: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("memory: do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("memory: unexpected status %d", resp.StatusCode)
	}
	return nil
}
