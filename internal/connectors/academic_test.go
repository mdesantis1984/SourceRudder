package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func TestAcademicSendsProvenEnginesAndNoCategories(t *testing.T) {
	var mu sync.Mutex
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got = r.URL.Query()
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Paper","url":"https://openalex.org/W1","content":"abs","engine":"openalex"}],"unresponsive_engines":[]}`))
	}))
	defer srv.Close()

	resp, err := NewAcademicConnector(srv.URL, cache.NewService(300)).Search(context.Background(),
		&types.SearchRequest{Query: "t4-academic-params", TimeRange: "year"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Results) != 1 || resp.Results[0].URL != "https://openalex.org/W1" {
		t.Fatalf("results = %+v, want the openalex.org result kept", resp.Results)
	}

	mu.Lock()
	defer mu.Unlock()
	want := map[string]string{
		"q":          "t4-academic-params",
		"format":     "json",
		"engines":    "arxiv,crossref,openalex,pubmed,semantic scholar,openairepublications",
		"language":   "en",
		"time_range": "year",
	}
	for k, v := range want {
		if got.Get(k) != v {
			t.Errorf("param %s = %q, want %q", k, got.Get(k), v)
		}
	}
	if _, ok := got["categories"]; ok {
		t.Errorf("categories must not be sent, got %q", got.Get("categories"))
	}
	if len(got) != len(want) {
		t.Errorf("unexpected params: %v", got)
	}
}

func TestIsAcademicURLHosts(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://openalex.org/W2741809807", true},
		{"https://europepmc.org/article/MED/12345", true},
		{"https://www.ncbi.nlm.nih.gov/pmc/articles/PMC123/", true},
		{"https://explore.openaire.eu/search/publication?pid=x", true},
		{"https://api.crossref.org/works/10.1/abc", true},
		{"https://OpenAlex.org/W1", true},
		{"https://arxiv.org/abs/2401.00001", true},
		{"https://doi.org/10.1000/xyz", true},
		{"https://pubmed.ncbi.nlm.nih.gov/123/", true},
		{"https://example.com/blog/post", false},
		{"https://news.example.org/story", false},
	}
	for _, tc := range cases {
		if got := isAcademicURL(tc.url); got != tc.want {
			t.Errorf("isAcademicURL(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}
