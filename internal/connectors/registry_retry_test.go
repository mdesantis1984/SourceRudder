package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// Registry APIs fail transiently from CT201 (Starlink micro-outages took DNS
// and IPv4 down together on 2026-11-25): one 503 must not empty the search.
func TestNPMRetriesOnceOnTransientFailure(t *testing.T) {
	old := retryBackoff
	retryBackoff = time.Millisecond
	defer func() { retryBackoff = old }()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"objects":[{"package":{"name":"zod","description":"schemas","links":{"homepage":"https://zod.dev"}}}]}`))
	}))
	defer srv.Close()
	c := NewNPMConnector(cache.NewService(300))
	c.baseURL = srv.URL
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "zod-retry", MaxResults: 3})
	if err != nil || len(resp.Results) != 1 || calls.Load() != 2 {
		t.Fatalf("err=%v results=%d calls=%d", err, len(resp.Results), calls.Load())
	}
}
