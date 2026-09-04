package connectors

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

func TestDirectConnectorsClassifyDNSTimeoutAsPartial(t *testing.T) {
	connectors := []struct {
		name string
		run  func(*http.Client) (*types.SearchResponse, error)
	}{
		{"reddit", func(client *http.Client) (*types.SearchResponse, error) {
			c := NewRedditConnector(RedditConfig{BaseURL: "https://reddit.invalid"}, cache.NewService(60))
			c.client = client
			return c.Search(context.Background(), &types.SearchRequest{Query: "dns-timeout-reddit"})
		}},
		{"stackoverflow", func(client *http.Client) (*types.SearchResponse, error) {
			c := NewStackOverflowConnector(cache.NewService(60))
			c.httpClient = client
			return c.Search(context.Background(), &types.SearchRequest{Query: "dns-timeout-stackoverflow"})
		}},
	}

	for _, tc := range connectors {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("dial tcp: lookup upstream: i/o timeout")
			})}
			resp, err := tc.run(client)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !resp.Partial {
				t.Fatal("expected DNS timeout to be classified as partial")
			}
			if len(resp.Warnings) == 0 || !strings.Contains(resp.Warnings[0], "timeout") {
				t.Fatalf("expected timeout warning, got %#v", resp.Warnings)
			}
			if len(resp.Errors) != 0 {
				t.Fatalf("transport degradation must not be a total failure: errors=%v", resp.Errors)
			}
			if calls != 1 {
				t.Fatalf("expected exactly one transport call (no retries), got %d", calls)
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
