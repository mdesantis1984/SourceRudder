package connectors

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// S8: connectors keep every result they already received; the MCP handler
// applies the caller's page size.

func TestOfficialDocsConnectorKeepsEveryApprovedResult(t *testing.T) {
	results := make([]types.SearchResultItem, 0, 15)
	for i := 0; i < 15; i++ {
		results = append(results, types.SearchResultItem{URL: fmt.Sprintf("https://go.dev/doc/%d", i), Source: "searxng"})
	}
	backend := &officialDocsBackendStub{responses: []*types.SearchResponse{{Results: results}}}
	resp, err := NewOfficialDocsConnector(backend).Search(context.Background(), &types.SearchRequest{Query: "golang documentation", MaxResults: 5, Offset: 3})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) != 15 || resp.Results[0].URL != "https://go.dev/doc/0" || resp.Results[14].URL != "https://go.dev/doc/14" {
		t.Fatalf("expected all 15 approved results in order, got %d", len(resp.Results))
	}
	if backend.requests[0].Offset != 3 {
		t.Fatalf("offset must reach the backend, got %d", backend.requests[0].Offset)
	}
}

func TestPyPIParseSimpleIndexKeepsMatchesUpToStoredBound(t *testing.T) {
	var html strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&html, `<a href="https://pypi.org/project/requests-%03d">requests-%03d</a>`, i, i)
	}
	c := &PyPIConnector{}
	results, err := c.parseSimpleIndex(html.String(), "requests", 5)
	if err != nil {
		t.Fatalf("parseSimpleIndex: %v", err)
	}
	if len(results) != pypiStoredResults {
		t.Fatalf("results=%d want the stored bound %d", len(results), pypiStoredResults)
	}
	if results[0].Title != "requests-000" || results[len(results)-1].Title != fmt.Sprintf("requests-%03d", pypiStoredResults-1) {
		t.Fatalf("order changed: first=%q last=%q", results[0].Title, results[len(results)-1].Title)
	}
}

func TestResolveMaxResults(t *testing.T) {
	cases := []struct {
		source    string
		requested int
		want      int
	}{
		{"web", 0, 20}, {"web", -1, 20}, {"web", 7, 7}, {"web", 500, 100},
		{"github", 0, 10}, {"npm", 500, 100}, {"npm", 3, 3},
		{"local_index", 0, 10}, {"local_index", 500, 50},
	}
	for _, tc := range cases {
		if got := ResolveMaxResults(tc.source, tc.requested); got != tc.want {
			t.Errorf("ResolveMaxResults(%q, %d) = %d, want %d", tc.source, tc.requested, got, tc.want)
		}
	}
}
