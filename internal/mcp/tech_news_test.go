package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/connectors"
)

// S7: search_tech_news is a registered search tool backed by the technews
// connector, with the shared input schema, annotations and outputSchema.
func TestSearchTechNewsToolIsListedLikeItsNeighbours(t *testing.T) {
	tool, ok := listedTools(t)["search_tech_news"]
	if !ok {
		t.Fatal("search_tech_news missing from tools/list")
	}
	desc, _ := tool["description"].(string)
	lower := strings.ToLower(desc)
	for _, want := range []string{"hacker news", "google news", "rss"} {
		if !strings.Contains(lower, want) {
			t.Errorf("description must name its sources (missing %q): %s", want, desc)
		}
	}
	if len(desc) > 330 {
		t.Errorf("description is %d bytes; keep it short", len(desc))
	}
	if tool["title"] != "Tech news search" || tool["outputSchema"] == nil {
		t.Errorf("title=%v outputSchema=%v", tool["title"], tool["outputSchema"] != nil)
	}
	ann, _ := tool["annotations"].(map[string]interface{})
	if ann["readOnlyHint"] != true || ann["openWorldHint"] != true {
		t.Errorf("annotations = %v", ann)
	}
	props, _ := tool["inputSchema"].(map[string]interface{})["properties"].(map[string]interface{})
	for _, p := range []string{"query", "maxResults", "language", "timeRange", "cursor", "format", "detail"} {
		if _, ok := props[p]; !ok {
			t.Errorf("input schema lacks %q", p)
		}
	}
}

func TestSearchTechNewsToolServesAndPagesTheTechNewsConnector(t *testing.T) {
	var items strings.Builder
	now := time.Now().UTC()
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&items, `<item><title>Rust story %d</title><link>https://feed.example/%d</link><pubDate>%s</pubDate></item>`,
			i, i, now.Add(-time.Duration(i+1)*time.Hour).Format(time.RFC1123Z))
	}
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel>` + items.String() + `</channel></rss>`))
	}))
	defer feed.Close()
	hn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"hits":[]}`)) }))
	defer hn.Close()
	gn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<rss><channel></channel></rss>`))
	}))
	defer gn.Close()

	conn := connectors.NewTechNewsConnector(cache.NewService(300), connectors.TechNewsConfig{
		HNURL: hn.URL, GoogleNewsURL: gn.URL, Feeds: []connectors.TechFeed{{Name: "infoq", URL: feed.URL}},
	})
	s := newResultSetServer(t, conn)
	resp := mustPaged(t, s, "search_tech_news", map[string]interface{}{"query": "rust", "maxResults": 5})
	if len(resp.Results) != 5 || resp.Total == nil || *resp.Total != 12 || resp.NextCursor == "" {
		t.Fatalf("len=%d total=%v next=%q", len(resp.Results), resp.Total, resp.NextCursor)
	}
	if resp.Results[0].Engine != "infoq" || resp.Results[0].Type != "news" {
		t.Errorf("first result = %+v", resp.Results[0])
	}
	rest := mustPaged(t, s, "search_tech_news", map[string]interface{}{"query": "rust", "maxResults": 20, "cursor": resp.NextCursor})
	if len(rest.Results) != 7 || rest.NextCursor != "" {
		t.Fatalf("rest len=%d next=%q", len(rest.Results), rest.NextCursor)
	}
}
