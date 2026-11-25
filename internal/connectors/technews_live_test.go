package connectors

import (
	"context"
	"os"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// TestTechNewsLive hits the real HN Algolia, Google News and the 19 curated
// feeds. It is skipped unless SOURCERUDDER_LIVE_NEWS=1, so the default suite
// never needs the network.
func TestTechNewsLive(t *testing.T) {
	if os.Getenv("SOURCERUDDER_LIVE_NEWS") != "1" {
		t.Skip("set SOURCERUDDER_LIVE_NEWS=1 to query the real news sources")
	}
	c := NewTechNewsConnector(cache.NewService(300), TechNewsConfig{})
	for _, req := range []*types.SearchRequest{
		{Query: "kubernetes", MaxResults: 15},
		{Query: "arquitectura de software", Language: "es", TimeRange: "month", MaxResults: 15},
	} {
		resp, err := c.Search(context.Background(), req)
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		engines := map[string]int{}
		for i, r := range resp.Results {
			if i < 15 {
				engines[r.Engine]++
			}
		}
		t.Logf("%q: results=%d partial=%v first-15 engines=%v warnings=%v", req.Query, len(resp.Results), resp.Partial, engines, resp.Warnings)
		for i, r := range resp.Results {
			if i >= 15 {
				break
			}
			t.Logf("%2d %-22s %s | %s", i, r.Engine, r.Title, r.URL)
		}
		if len(resp.Results) == 0 {
			t.Errorf("%q: no results from any live source", req.Query)
		}
		if resp.Partial {
			t.Errorf("%q: both query sources failed: %v", req.Query, resp.Warnings)
		}
	}
}
