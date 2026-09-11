package connectors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func TestImagesConnectorRanksQualifiedCandidatesBeforeLimiting(t *testing.T) {
	srv := imagesFixtureServer(t, []map[string]interface{}{
		{"title": "UX Honeycomb Diagram", "url": "https://example.test/ux", "img_src": "https://cdn.example.test/ux.jpg"},
		{"title": "Ceylon icon", "url": "https://example.test/ceylon", "img_src": "https://cdn.example.test/ceylon.png"},
		{"title": "Generic asset", "url": "https://example.test/spam", "img_src": "https://cdn.example.test/spam.jpg", "content": strings.Repeat("marketing copy ", 40) + "container architecture diagram"},
		{"title": "Docker Container Architecture", "url": "https://example.test/docker", "img_src": "https://cdn.example.test/docker.jpg"},
		{"title": "C4 Container Diagram", "url": "https://example.test/c4", "img_src": "https://cdn.example.test/c4.jpg"},
	})
	defer srv.Close()

	resp, err := NewImagesConnector(srv.URL, cache.NewService(60)).Search(context.Background(), &types.SearchRequest{
		Query: "container architecture diagram", MaxResults: 2,
	})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got, want := imageTitles(resp.Results), []string{"Docker Container Architecture", "C4 Container Diagram"}; !sameStrings(got, want) {
		t.Fatalf("selected titles = %v, want %v", got, want)
	}
}

func TestImagesConnectorRetainsRelevantSVGAndSafeFallbacks(t *testing.T) {
	srv := imagesFixtureServer(t, []map[string]interface{}{
		{"title": "Solar Eclipse Icon", "url": "https://example.test/eclipse", "img_src": "https://cdn.example.test/eclipse.svg"},
		{"title": "Solar Eclipse photo", "url": "https://cdn.example.test/photo.jpeg"},
		{"title": "Solar Eclipse broken", "url": "https://example.test/page", "img_src": "://bad"},
		{"title": "Solar Eclipse page", "url": "https://example.test/page"},
	})
	defer srv.Close()

	resp, err := NewImagesConnector(srv.URL, cache.NewService(60)).Search(context.Background(), &types.SearchRequest{Query: "solar eclipse", MaxResults: 10})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got, want := imageTitles(resp.Results), []string{"Solar Eclipse Icon", "Solar Eclipse photo"}; !sameStrings(got, want) {
		t.Fatalf("selected titles = %v, want %v", got, want)
	}
}

func TestImagesConnectorUsesWholeWordsWithoutDiscardingValidFallbacks(t *testing.T) {
	srv := imagesFixtureServer(t, []map[string]interface{}{
		{"title": "Education diagram", "url": "https://example.test/education", "img_src": "https://cdn.example.test/education.jpg"},
		{"title": "Cat portrait", "url": "https://example.test/cat", "img_src": "https://cdn.example.test/cat.jpg"},
		{"title": "Northern Lights photograph", "url": "https://example.test/lights", "img_src": "https://cdn.example.test/lights.jpg"},
		{"title": "Aurora borealis", "url": "https://example.test/aurora", "img_src": "https://cdn.example.test/aurora.jpg"},
	})
	defer srv.Close()

	connector := NewImagesConnector(srv.URL, cache.NewService(60))
	cat, err := connector.Search(context.Background(), &types.SearchRequest{Query: "cat cat", MaxResults: 2})
	if err != nil {
		t.Fatalf("cat Search() error = %v", err)
	}
	if got, want := imageTitles(cat.Results), []string{"Cat portrait", "Education diagram"}; !sameStrings(got, want) {
		t.Fatalf("whole-word ranking = %v, want %v", got, want)
	}

	lights, err := connector.Search(context.Background(), &types.SearchRequest{Query: "northern lights", MaxResults: 4})
	if err != nil {
		t.Fatalf("lights Search() error = %v", err)
	}
	if got := imageTitles(lights.Results); !containsString(got, "Northern Lights photograph") || !containsString(got, "Aurora borealis") {
		t.Fatalf("valid lexical fallback was discarded under the result cap: got %v", got)
	}
}

func TestImagesConnectorStripsSearxngHighlightMarkersBeforeMatchingAndOutput(t *testing.T) {
	srv := imagesFixtureServer(t, []map[string]interface{}{
		{"title": "Edu\uE000cat\uE001ion illustration", "url": "https://example.test/education", "img_src": "https://cdn.example.test/education.jpg", "content": "Education material"},
		{"title": "\uE000Cat\uE001 portrait", "url": "https://example.test/cat", "img_src": "https://cdn.example.test/cat.jpg", "content": "A \uE000cat\uE001 in daylight"},
	})
	defer srv.Close()

	resp, err := NewImagesConnector(srv.URL, cache.NewService(60)).Search(context.Background(), &types.SearchRequest{Query: "cat", MaxResults: 2})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got, want := imageTitles(resp.Results), []string{"Cat portrait", "Education illustration"}; !sameStrings(got, want) {
		t.Fatalf("marker-normalized titles = %v, want %v", got, want)
	}
	if strings.ContainsAny(resp.Results[0].Title+resp.Results[0].Snippet, "\uE000\uE001") || !strings.Contains(resp.Results[0].Snippet, "A cat in daylight") || !strings.Contains(resp.Results[0].Snippet, "https://example.test/cat") {
		t.Fatalf("marker-free visible context was not preserved: %#v", resp.Results[0])
	}
}

func TestImagesConnectorRejectsUnsafeURLsAndCanonicalizesDeduplicatedImages(t *testing.T) {
	srv := imagesFixtureServer(t, []map[string]interface{}{
		{"title": "Cat userinfo", "url": "https://example.test/userinfo", "img_src": "https://user@cdn.example.test/cat.jpg"},
		{"title": "Cat empty host", "url": "https://example.test/empty", "img_src": "https://:443/cat.jpg"},
		{"title": "Cat page", "url": "https://example.test/page.html#.jpg"},
		{"title": "Cat extensionless", "url": "https://example.test/extensionless", "img_src": "https://cdn.example.test/render"},
		{"title": "Cat ppm", "url": "https://example.test/ppm", "img_src": "https://cdn.example.test/cat.ppm"},
		{"title": "Cat signed", "url": "https://example.test/signed", "img_src": "HTTPS://CDN.EXAMPLE.TEST/cat.jpg?sig=one#preview"},
		{"title": "Cat duplicate", "url": "https://example.test/duplicate", "img_src": "https://cdn.example.test/cat.jpg?sig=one#other"},
		{"title": "Cat transform 200", "url": "https://example.test/200", "img_src": "https://cdn.example.test/cat.jpg?w=200"},
		{"title": "Cat transform 400", "url": "https://example.test/400", "img_src": "https://cdn.example.test/cat.jpg?w=400"},
	})
	defer srv.Close()

	connector := NewImagesConnector(srv.URL, cache.NewService(60))
	first, err := connector.Search(context.Background(), &types.SearchRequest{Query: "cat", MaxResults: 10})
	if err != nil {
		t.Fatalf("first Search() error = %v", err)
	}
	if got, want := imageTitles(first.Results), []string{"Cat extensionless", "Cat ppm", "Cat signed", "Cat transform 200", "Cat transform 400"}; !sameStrings(got, want) {
		t.Fatalf("validated/deduplicated titles = %v, want %v", got, want)
	}
	if first.Results[2].URL != "https://cdn.example.test/cat.jpg?sig=one" || first.Results[2].CitationID == "" {
		t.Fatalf("fragment was not normalized into a stable canonical image: %#v", first.Results[2])
	}
	second, err := connector.Search(context.Background(), &types.SearchRequest{Query: "portrait", MaxResults: 10})
	if err != nil {
		t.Fatalf("second Search() error = %v", err)
	}
	if first.Results[2].CitationID != second.Results[2].CitationID || second.Results[3].URL == second.Results[4].URL {
		t.Fatalf("citation or transform URL identity is unstable: first=%#v second=%#v", first.Results[2], second.Results)
	}
}

func TestImagesConnectorPreservesBoundedContextAndCachedPartialMetadata(t *testing.T) {
	content := "Solar eclipse photograph with a visible corona. " + strings.Repeat("irrelevant caption ", 100)
	previewURL := "https://preview.example.test/eclipse.png?signature=indexed-preview"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeImagesFixture(t, w, []map[string]interface{}{{
			"title": "Solar Eclipse Photograph", "url": "https://example.test/eclipse", "img_src": "https://cdn.example.test/eclipse.jpg", "thumbnail_src": previewURL, "content": content, "source": "example", "engine": "bing",
		}}, [][]interface{}{{"slow-engine", "timeout"}})
	}))
	defer srv.Close()

	connector := NewImagesConnector(srv.URL, cache.NewService(60))
	request := &types.SearchRequest{Query: "solar eclipse"}
	first, err := connector.Search(context.Background(), request)
	if err != nil {
		t.Fatalf("first Search() error = %v", err)
	}
	cached, err := connector.Search(context.Background(), request)
	if err != nil {
		t.Fatalf("cached Search() error = %v", err)
	}
	if len(first.Results) != 1 || !strings.Contains(first.Results[0].Snippet, "visible corona") || !strings.Contains(first.Results[0].Snippet, "https://example.test/eclipse") || !strings.Contains(first.Results[0].Snippet, "Indexed preview: "+previewURL) || len([]rune(first.Results[0].Snippet)) > 600 {
		t.Fatalf("first result did not preserve bounded context: %#v", first.Results)
	}
	if !cached.Cached || !cached.Partial || len(cached.Warnings) != 1 || !strings.Contains(cached.Warnings[0], "slow-engine") || cached.Results[0].Snippet != first.Results[0].Snippet {
		t.Fatalf("cached partial response lost context or degradation metadata: %#v", cached)
	}
}

func TestImagesConnectorOmitsInvalidOrDuplicateIndexedPreview(t *testing.T) {
	tests := []struct {
		name      string
		thumbnail string
	}{
		{name: "userinfo preview", thumbnail: "https://user@preview.example.test/image.png"},
		{name: "malformed preview", thumbnail: "://preview.example.test/image.png"},
		{name: "non HTTP preview", thumbnail: "ftp://preview.example.test/image.png"},
		{name: "duplicate preview", thumbnail: "https://cdn.example.test/image.jpg"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := imagesFixtureServer(t, []map[string]interface{}{{
				"title": "Indexed Diagram", "url": "https://source.example.test/diagram", "img_src": "https://cdn.example.test/image.jpg", "thumbnail_src": tt.thumbnail,
			}})
			defer srv.Close()

			resp, err := NewImagesConnector(srv.URL, cache.NewService(60)).Search(context.Background(), &types.SearchRequest{Query: "indexed diagram", MaxResults: 1})
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if len(resp.Results) != 1 || strings.Contains(resp.Results[0].Snippet, "Indexed preview:") {
				t.Fatalf("indexed preview handling = %#v", resp.Results)
			}
		})
	}
}

func TestImagesConnectorBoundsContextWithoutCuttingURLs(t *testing.T) {
	sourceURL := "https://source.example.test/" + strings.Repeat("s", 180)
	previewURL := "https://preview.example.test/image.png?signature=" + strings.Repeat("p", 180)
	srv := imagesFixtureServer(t, []map[string]interface{}{{
		"title": "Bounded Diagram", "url": sourceURL, "img_src": "https://cdn.example.test/image.jpg", "thumbnail_src": previewURL, "content": strings.Repeat("descriptive context ", 80),
	}})
	defer srv.Close()

	resp, err := NewImagesConnector(srv.URL, cache.NewService(60)).Search(context.Background(), &types.SearchRequest{Query: "bounded diagram", MaxResults: 1})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("results = %#v", resp.Results)
	}
	snippet := resp.Results[0].Snippet
	if len([]rune(snippet)) > 600 || !strings.Contains(snippet, "Source page: "+sourceURL) || !strings.Contains(snippet, "Indexed preview: "+previewURL) {
		t.Fatalf("bounded context cut or omitted a URL: %q", snippet)
	}
}

func TestImagesConnectorForwardsOptionsAndIsolatesCache(t *testing.T) {
	var mu sync.Mutex
	var requests []map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, map[string]string{
			"language": r.URL.Query().Get("language"), "time_range": r.URL.Query().Get("time_range"), "safesearch": r.URL.Query().Get("safesearch"),
		})
		mu.Unlock()
		writeImagesFixture(t, w, []map[string]interface{}{{"title": "Solar Eclipse", "url": "https://example.test/eclipse", "img_src": "https://cdn.example.test/eclipse.jpg"}}, nil)
	}))
	defer srv.Close()

	connector := NewImagesConnector(srv.URL, cache.NewService(60))
	requestsToMake := []*types.SearchRequest{
		{Query: "solar eclipse", MaxResults: 1, Language: "es", SafeSearch: true, TimeRange: "week"},
		{Query: "solar eclipse", MaxResults: 2, Language: "es", SafeSearch: true, TimeRange: "week"},
		{Query: "solar eclipse", MaxResults: 2, Language: "en", SafeSearch: true, TimeRange: "week"},
		{Query: "solar eclipse", MaxResults: 2, Language: "en", SafeSearch: true, TimeRange: "month"},
		{Query: "solar eclipse", MaxResults: 2, Language: "en", SafeSearch: false, TimeRange: "month"},
	}
	for _, request := range requestsToMake {
		if _, err := connector.Search(context.Background(), request); err != nil {
			t.Fatalf("Search() error = %v", err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != len(requestsToMake) {
		t.Fatalf("upstream requests = %d, want %d; options must not share cached responses", len(requests), len(requestsToMake))
	}
	if got := requests[0]; got["language"] != "es" || got["time_range"] != "week" || got["safesearch"] != "1" {
		t.Fatalf("non-default options were not forwarded: %#v", got)
	}
	if got := requests[len(requests)-1]["safesearch"]; got != "0" {
		t.Fatalf("safeSearch=false must be forwarded as 0, got %q", got)
	}
}

func imagesFixtureServer(t *testing.T, results []map[string]interface{}) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeImagesFixture(t, w, results, nil)
	}))
}

func writeImagesFixture(t *testing.T, w http.ResponseWriter, results []map[string]interface{}, unresponsive [][]interface{}) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{"results": results, "unresponsive_engines": unresponsive}); err != nil {
		t.Errorf("encode fixture: %v", err)
	}
}

func imageTitles(results []types.SearchResultItem) []string {
	titles := make([]string, len(results))
	for i, result := range results {
		titles[i] = result.Title
	}
	return titles
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
