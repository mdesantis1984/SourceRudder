package connectors

import (
	"context"
	"testing"

	"github.com/thiscloud/ia-buscar/pkg/types"
)

func TestLocalIndexSearchRanksDeterministically(t *testing.T) {
	path := writeLocalIndexCorpus(t, `{
  "version": 1,
  "documents": [
    {"id":"runbook","title":"Operations runbook","snippet":"Diagnose an HTTP timeout safely.","tags":["network"]},
    {"id":"guide","title":"HTTP timeout guide","url":"https://example.com/guide","snippet":"Client configuration.","author":"Example","tags":["go"]},
    {"id":"other","title":"SearXNG setup","snippet":"Search configuration.","tags":["search"]}
  ]
}`)
	connector, err := NewLocalIndexConnector(path)
	if err != nil {
		t.Fatalf("NewLocalIndexConnector: %v", err)
	}
	resp, err := connector.Search(context.Background(), &types.SearchRequest{Query: "HTTP timeout", MaxResults: 1})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Strategy != "local_index_lexical" || len(resp.Results) != 1 {
		t.Fatalf("unexpected response: strategy=%q results=%d", resp.Strategy, len(resp.Results))
	}
	result := resp.Results[0]
	if result.CitationID != "local-index:guide" || result.URL != "https://example.com/guide" {
		t.Fatalf("unexpected top result: %+v", result)
	}
	if result.Source != "local_index" || result.Type != "document" || result.Score <= 0 {
		t.Fatalf("missing local-index metadata: %+v", result)
	}

	empty, err := connector.Search(context.Background(), &types.SearchRequest{Query: "missing term"})
	if err != nil {
		t.Fatalf("empty Search: %v", err)
	}
	if empty.Results == nil || len(empty.Results) != 0 || empty.Partial || len(empty.Errors) != 0 {
		t.Fatalf("no match must be a healthy empty result: %+v", empty)
	}
}

func TestLocalIndexGeneratesOpaqueURL(t *testing.T) {
	path := writeLocalIndexCorpus(t, `{"version":1,"documents":[{"id":"safe-id","title":"Safe document"}]}`)
	connector, err := NewLocalIndexConnector(path)
	if err != nil {
		t.Fatalf("NewLocalIndexConnector: %v", err)
	}
	resp, err := connector.Search(context.Background(), &types.SearchRequest{Query: "safe"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got := resp.Results[0].URL; got != "local-index://document/safe-id" {
		t.Fatalf("generated URL=%q", got)
	}
}
