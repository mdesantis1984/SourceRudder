package resultfilter

import (
	"testing"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func TestHasValidHost(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://en.wikipedia.org/wiki/Paris", true},
		{"http://example.com", true},
		{"HTTPS://Example.com/x", true},
		{"http:///go/v1/AaKPaj", false},
		{"https:///path", false},
		{"/relative/path", false},
		{"", false},
		{"ftp://example.com/file", false},
		{"javascript:alert(1)", false},
		{"https://", false},
		{"http://:8080/x", false},
		{"%zz", false},
	}
	for _, tc := range cases {
		if got := HasValidHost(tc.url); got != tc.want {
			t.Errorf("HasValidHost(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

func TestIsAdultHost(t *testing.T) {
	adult := []string{
		"xnxx.com", "forum.xnxx.com", "www.xvideos.com", "pornhub.com", "de.pornhub.com",
		"xhamster.com", "redtube.com", "youporn.com", "spankbang.com", "eporner.com",
		"chaturbate.com", "onlyfans.com", "brazzers.com", "rule34.xxx", "www.rule34.xxx",
		"free-porn-videos.net", "best.xxx", "hentai.example.org", "nsfw.reddit.example",
		"XNXX.COM", "forum.xnxx.com.",
	}
	for _, h := range adult {
		if !IsAdultHost(h) {
			t.Errorf("IsAdultHost(%q) = false, want true", h)
		}
	}
	clean := []string{
		"essex.ac.uk", "sussex.ac.uk", "www.essex.ac.uk", "en.wikipedia.org", "go.dev",
		"sexton.example.com", "middlesex.edu", "example.com", "notxnxx.com", "xnxx.com.example.org",
		"", "localhost", "unisex-clothes.example.com",
		// "sex" alone is no longer an adult marker: it removed sex-education
		// sites.
		"sex.example.com",
	}
	for _, h := range clean {
		if IsAdultHost(h) {
			t.Errorf("IsAdultHost(%q) = true, want false", h)
		}
	}
}

func item(url string) types.SearchResultItem {
	return types.SearchResultItem{Title: "t", URL: url}
}

func TestFilterCounts(t *testing.T) {
	in := []types.SearchResultItem{
		item("https://go.dev/doc"),
		item("http:///go/v1/abc"),
		item("https://forum.xnxx.com/thread/1"),
		item("https://essex.ac.uk/a"),
		item("https://sussex.ac.uk/b"),
	}
	kept, noHost, adult := Filter(in, true)
	if noHost != 1 || adult != 1 || len(kept) != 3 {
		t.Fatalf("safe on: kept=%d noHost=%d adult=%d", len(kept), noHost, adult)
	}
	for _, k := range kept {
		if k.URL == "https://forum.xnxx.com/thread/1" || k.URL == "http:///go/v1/abc" {
			t.Errorf("unexpected kept %q", k.URL)
		}
	}
	kept, noHost, adult = Filter(in, false)
	if noHost != 1 || adult != 0 || len(kept) != 4 {
		t.Fatalf("safe off: kept=%d noHost=%d adult=%d", len(kept), noHost, adult)
	}
	if len(in) != 5 || in[1].URL != "http:///go/v1/abc" {
		t.Fatalf("input slice must not be modified: %v", in)
	}
}

func TestFilterKeepsOrderAndNilSafe(t *testing.T) {
	kept, a, b := Filter(nil, true)
	if len(kept) != 0 || a != 0 || b != 0 {
		t.Fatalf("nil input: %v %d %d", kept, a, b)
	}
	in := []types.SearchResultItem{item("https://b.example.com"), item("https://a.example.com")}
	kept, _, _ = Filter(in, true)
	if kept[0].URL != "https://b.example.com" || kept[1].URL != "https://a.example.com" {
		t.Fatalf("order changed: %v", kept)
	}
}

func TestWarnings(t *testing.T) {
	if got := NoHostWarning(3); got != "removed 3 results without a valid URL host (cloaked engine links, searxng#6967)" {
		t.Errorf("NoHostWarning = %q", got)
	}
	if got := AdultWarning(2); got != "removed 2 results from adult domains (safe search)" {
		t.Errorf("AdultWarning = %q", got)
	}
}

func TestDistinctHosts(t *testing.T) {
	items := []types.SearchResultItem{
		item("https://a.example.com/1"), item("https://A.example.com/2"), item("https://b.example.com"), item("not a url"),
	}
	if got := DistinctHosts(items); got != 2 {
		t.Errorf("DistinctHosts = %d, want 2", got)
	}
}

// Brand labels match on any TLD or numbered mirror, adult TLDs match, and
// legitimate hosts that merely contain "porn" or "sex" are kept.
func TestAdultHostBrandsTLDsAndKnownFalsePositives(t *testing.T) {
	for _, h := range []string{"xvideos.es", "xvideos2.com", "xhamster.desi", "xhamster2.com", "redtube.com.br",
		"chaturbate.global", "spankbang.party", "example.adult", "example.sexy", "example.porn", "pornhub.org"} {
		if !IsAdultHost(h) {
			t.Errorf("%s must be adult", h)
		}
	}
	for _, h := range []string{"ville-pornic.fr", "pornichet.fr", "pornography-studies.org", "sex-education.org",
		"sex-ed.example.org", "essex.ac.uk", "sussex.ac.uk", "middlesex.edu", "unisex.com", "go.dev"} {
		if IsAdultHost(h) {
			t.Errorf("%s must not be adult", h)
		}
	}
}

// Hosts seen live for "xnxx forum" with safe search on (2026-11-25): brands
// joined with '-' and labels containing "porn" are adult, while the Pornic
// town hosts and pornography studies stay.
func TestAdultHostLiveLeaksAndPornicExceptions(t *testing.T) {
	for _, h := range []string{"m-xnxx.com", "xnxx-com.us", "forum.pornchat.com", "www.youpornlist.com",
		"theporndude.com", "porngeek.com", "megaporn.example.com"} {
		if !IsAdultHost(h) {
			t.Errorf("%s must be adult", h)
		}
	}
	for _, h := range []string{"ville-pornic.fr", "pornichet.fr", "www.pornic.com", "pornography-studies.org"} {
		if IsAdultHost(h) {
			t.Errorf("%s must not be adult", h)
		}
	}
}

// Titles seen live for "free porn videos" with safe search on (2026-11-25):
// generic hosts carry whole-word adult terms in the title. Words that only
// contain them (pornography, Pornic) and safe search off keep the result.
func TestFilterDropsAdultTitlesOnlyWithSafeSearch(t *testing.T) {
	items := []types.SearchResultItem{
		{URL: "https://xlxx.icu/", Title: "Free Porn XLXX.com - XXX videos"},
		{URL: "https://nudzy.net/", Title: "Nudzy — Free Amateur Porn Videos"},
		{URL: "https://www.tiava.com/", Title: "Free Porn Videos – Tiava"},
		{URL: "https://example.org/law", Title: "New pornography law passed in the UK"},
		{URL: "https://pornic.example.fr/", Title: "Pornic, France: tourism guide"},
		{URL: "https://go.dev/blog/context", Title: "Go Concurrency Patterns: Context"},
	}
	kept, _, adult := Filter(items, true)
	if adult != 3 || len(kept) != 3 || kept[0].URL != "https://example.org/law" {
		t.Fatalf("safe search on: adult=%d kept=%v", adult, kept)
	}
	if kept, _, adult := Filter(items, false); adult != 0 || len(kept) != len(items) {
		t.Fatalf("safe search off must keep every titled result: adult=%d kept=%d", adult, len(kept))
	}
}
