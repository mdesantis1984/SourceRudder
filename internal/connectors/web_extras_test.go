package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// extrasSearxng serves one fixed JSON body for page 1 and a different one for
// every other page, and counts the requests.
func extrasSearxng(t *testing.T, page1, other string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if p := r.URL.Query().Get("pageno"); p == "" || p == "1" {
			_, _ = w.Write([]byte(page1))
			return
		}
		_, _ = w.Write([]byte(other))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const extrasPage1 = `{
 "results":[{"title":"t","url":"https://example.test/1","content":"c","engine":"bing"}],
 "unresponsive_engines":[],
 "answers":["  42 \n is   the answer ", {"answer":"from object","url":"https://a.test"}, {"noanswer":1}, null, 5, "", "42 is the answer", {"answer":""}],
 "infoboxes":[
   {"infobox":"Albert Einstein","content":"Physicist\r\nborn 1879","id":"https://en.wikipedia.org/wiki/Albert_Einstein","urls":[{"title":"w","url":"https://other.test"}]},
   null,
   "not an object",
   {"infobox":"No id","content":"uses first url","urls":[{"title":"x"},{"url":"https://first.test"},{"url":"https://second.test"}]},
   {"infobox":"No id","content":"uses first url","urls":[{"url":"https://first.test"}]},
   {"infobox":"","content":""}
 ],
 "suggestions":[" go generics ","", "go generics", 7, "go\u2028modules"],
 "corrections":["golang","golang", null]
}`

func TestWebDecodesAndSanitizesSearxngExtras(t *testing.T) {
	srv, _ := extrasSearxng(t, extrasPage1, `{"results":[]}`)
	resp, err := NewWebConnector(srv.URL, cache.NewService(300)).Search(context.Background(),
		&types.SearchRequest{Query: "extras-decode", MaxResults: 1})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"42 is the answer", "from object"}; !reflect.DeepEqual(resp.Answers, want) {
		t.Errorf("answers = %q, want %q", resp.Answers, want)
	}
	wantBoxes := []types.Infobox{
		{Title: "Albert Einstein", Content: "Physicist born 1879", URL: "https://en.wikipedia.org/wiki/Albert_Einstein"},
		{Title: "No id", Content: "uses first url", URL: "https://first.test"},
	}
	if !reflect.DeepEqual(resp.Infoboxes, wantBoxes) {
		t.Errorf("infoboxes = %+v, want %+v", resp.Infoboxes, wantBoxes)
	}
	if want := []string{"go generics", "go modules"}; !reflect.DeepEqual(resp.Suggestions, want) {
		t.Errorf("suggestions = %q, want %q", resp.Suggestions, want)
	}
	if want := []string{"golang"}; !reflect.DeepEqual(resp.Corrections, want) {
		t.Errorf("corrections = %q, want %q", resp.Corrections, want)
	}
}

func TestWebExtrasSanitizeEveryString(t *testing.T) {
	long := strings.Repeat("é", 600)
	body := fmt.Sprintf(`{"results":[],"answers":[%q,%q],"infoboxes":[{"infobox":%q,"content":%q,"id":"https://x.test/a\u2029b"}],"suggestions":[%q],"corrections":["a\r\nb\u2028c\u2029d\te"]}`,
		"l1\nl2\rl3\u2028l4\u2029l5", long, "ti\ntle", long, long)
	srv, _ := extrasSearxng(t, body, `{"results":[]}`)
	resp, err := NewWebConnector(srv.URL, cache.NewService(300)).Search(context.Background(), &types.SearchRequest{Query: "extras-sanitize"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Answers) != 2 || resp.Answers[0] != "l1 l2 l3 l4 l5" {
		t.Fatalf("answers = %q", resp.Answers)
	}
	if n := utf8.RuneCountInString(resp.Answers[1]); n != 500 || !utf8.ValidString(resp.Answers[1]) {
		t.Errorf("long answer has %d runes (valid=%v), want 500", n, utf8.ValidString(resp.Answers[1]))
	}
	if len(resp.Infoboxes) != 1 {
		t.Fatalf("infoboxes = %+v", resp.Infoboxes)
	}
	box := resp.Infoboxes[0]
	if box.Title != "ti tle" || utf8.RuneCountInString(box.Content) != 500 || box.URL != "https://x.test/a b" {
		t.Errorf("infobox = %+v", box)
	}
	if n := utf8.RuneCountInString(resp.Suggestions[0]); n != 500 {
		t.Errorf("long suggestion has %d runes, want 500", n)
	}
	if !reflect.DeepEqual(resp.Corrections, []string{"a b c d e"}) {
		t.Errorf("corrections = %q", resp.Corrections)
	}
	all := append(append(append([]string{}, resp.Answers...), resp.Suggestions...), resp.Corrections...)
	all = append(all, box.Title, box.Content, box.URL)
	for _, s := range all {
		if strings.ContainsAny(s, "\r\n\u2028\u2029") || s != strings.TrimSpace(s) || strings.Contains(s, "  ") {
			t.Errorf("unsanitized string %q", s)
		}
	}
}

func TestWebOmitsExtrasWhenSearxngSendsNone(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `{"results":[{"title":"t","url":"https://example.test/1","content":"c","engine":"bing"}],"unresponsive_engines":[]}`,
		"empty":  `{"results":[],"answers":[],"infoboxes":[],"suggestions":[],"corrections":[]}`,
		"null":   `{"results":[],"answers":null,"infoboxes":null,"suggestions":null,"corrections":null}`,
		"wrong":  `{"results":[],"answers":"text","infoboxes":{"a":1},"suggestions":5,"corrections":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, _ := extrasSearxng(t, body, `{"results":[]}`)
			resp, err := NewWebConnector(srv.URL, cache.NewService(300)).Search(context.Background(), &types.SearchRequest{Query: "extras-none-" + name})
			if err != nil {
				t.Fatal(err)
			}
			if resp.Partial {
				t.Errorf("odd extras must not degrade the search")
			}
			wire, _ := json.Marshal(resp)
			for _, key := range []string{`"answers"`, `"infoboxes"`, `"suggestions"`, `"corrections"`} {
				if strings.Contains(string(wire), key) {
					t.Errorf("%s must be omitted: %s", key, wire)
				}
			}
		})
	}
}

func TestWebExtrasComeOnlyFromPageOne(t *testing.T) {
	page2 := `{"results":[{"title":"t2","url":"https://example.test/2","content":"c","engine":"bing"}],"answers":["page two answer"],"suggestions":["page two"],"corrections":["p2"],"infoboxes":[{"infobox":"P2","content":"c"}]}`
	srv, hits := extrasSearxng(t, extrasPage1, page2)
	resp, err := NewWebConnector(srv.URL, cache.NewService(300)).Search(context.Background(),
		&types.SearchRequest{Query: "extras-page1", MaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 || len(resp.Results) != 2 {
		t.Fatalf("expected page 2 to be fetched (hits=%d results=%d)", hits.Load(), len(resp.Results))
	}
	for _, a := range resp.Answers {
		if strings.Contains(a, "page two") {
			t.Errorf("page 2 answer leaked: %q", resp.Answers)
		}
	}
	if len(resp.Suggestions) != 2 || len(resp.Corrections) != 1 || len(resp.Infoboxes) != 2 {
		t.Errorf("extras must be page 1 only: %+v", resp)
	}
}

func TestWebExtrasSurviveEmptyResultsAndCache(t *testing.T) {
	srv, hits := extrasSearxng(t, `{"results":[],"answers":["sha256: abc"]}`, `{"results":[]}`)
	c := NewWebConnector(srv.URL, cache.NewService(300))
	req := &types.SearchRequest{Query: "extras-cache"}
	first, err := c.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Answers, []string{"sha256: abc"}) || len(first.Results) != 0 {
		t.Fatalf("first = %+v", first)
	}
	before := hits.Load()
	second, err := c.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Cached || hits.Load() != before {
		t.Fatalf("expected a cache hit without upstream traffic (cached=%v)", second.Cached)
	}
	if !reflect.DeepEqual(second.Answers, first.Answers) {
		t.Errorf("cache hit lost answers: %q", second.Answers)
	}

	srv2, _ := extrasSearxng(t, extrasPage1, `{"results":[]}`)
	c2 := NewWebConnector(srv2.URL, cache.NewService(300))
	req2 := &types.SearchRequest{Query: "extras-cache-full", MaxResults: 1}
	a, _ := c2.Search(context.Background(), req2)
	b, _ := c2.Search(context.Background(), req2)
	if !b.Cached {
		t.Fatal("second call must be cached")
	}
	a.Cached = false
	if !reflect.DeepEqual(a.Answers, b.Answers) || !reflect.DeepEqual(a.Infoboxes, b.Infoboxes) ||
		!reflect.DeepEqual(a.Suggestions, b.Suggestions) || !reflect.DeepEqual(a.Corrections, b.Corrections) {
		t.Errorf("cache round trip changed the extras:\nfirst=%+v\nsecond=%+v", a, b)
	}
}
