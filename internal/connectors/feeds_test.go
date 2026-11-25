package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

const rssFixture = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/">
 <channel>
  <title>Example feed</title>
  <item>
   <title>  Kubernetes   1.40 &amp; the
   new scheduler - Example Times </title>
   <link>https://example.test/a?utm_source=rss</link>
   <description>&lt;p&gt;Hello &lt;b&gt;world&lt;/b&gt;&nbsp;&amp;amp; friends&lt;/p&gt;&lt;script&gt;alert(1)&lt;/script&gt;</description>
   <pubDate>Mon, 02 Nov 2026 10:30:00 GMT</pubDate>
   <dc:creator>Ada Lovelace</dc:creator>
   <source url="https://example.test">Example Times</source>
  </item>
  <item>
   <title>Unsafe link</title>
   <link>javascript:alert(1)</link>
  </item>
  <item>
   <title></title>
   <link>https://example.test/no-title</link>
  </item>
  <item>
   <title>No date</title>
   <link>https://example.test/no-date</link>
   <description>plain</description>
  </item>
 </channel>
</rss>`

const atomFixture = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
 <title>Atom feed</title>
 <entry>
  <title type="html">Go 1.30 is &lt;em&gt;released&lt;/em&gt;</title>
  <link rel="self" href="https://example.test/self"/>
  <link rel="alternate" href="https://example.test/go-130"/>
  <published>2026-11-03T08:00:00Z</published>
  <updated>2026-11-04T08:00:00Z</updated>
  <author><name>The Go Team</name></author>
  <summary>Summary &lt;a href="x"&gt;text&lt;/a&gt;</summary>
 </entry>
 <entry>
  <title>Updated only</title>
  <link href="https://example.test/updated-only"/>
  <updated>2026-11-05T08:00:00+02:00</updated>
  <content type="html">&lt;p&gt;Body only&lt;/p&gt;</content>
 </entry>
</feed>`

func TestParseFeedRSS(t *testing.T) {
	items, err := parseFeed(strings.NewReader(rssFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 valid items (unsafe link and empty title dropped), got %d: %+v", len(items), items)
	}
	first := items[0]
	if first.Title != "Kubernetes 1.40 & the new scheduler - Example Times" {
		t.Errorf("title not sanitized: %q", first.Title)
	}
	if first.Link != "https://example.test/a?utm_source=rss" {
		t.Errorf("link = %q", first.Link)
	}
	if first.Summary != "Hello world & friends" {
		t.Errorf("summary not stripped of tags, script and entities: %q", first.Summary)
	}
	if want := time.Date(2026, 11, 2, 10, 30, 0, 0, time.UTC); !first.Published.Equal(want) {
		t.Errorf("published = %v", first.Published)
	}
	if first.Author != "Ada Lovelace" || first.SourceName != "Example Times" || first.SourceURL != "https://example.test" {
		t.Errorf("author/source = %q / %q / %q", first.Author, first.SourceName, first.SourceURL)
	}
	if !items[1].Published.IsZero() {
		t.Errorf("item without date must keep a zero time, got %v", items[1].Published)
	}
}

func TestParseFeedAtom(t *testing.T) {
	items, err := parseFeed(strings.NewReader(atomFixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 entries, got %+v", items)
	}
	if items[0].Title != "Go 1.30 is released" || items[0].Link != "https://example.test/go-130" {
		t.Errorf("entry 0 = %+v", items[0])
	}
	if items[0].Summary != "Summary text" || items[0].Author != "The Go Team" {
		t.Errorf("entry 0 summary/author = %q / %q", items[0].Summary, items[0].Author)
	}
	if want := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC); !items[0].Published.Equal(want) {
		t.Errorf("published must win over updated: %v", items[0].Published)
	}
	if items[1].Link != "https://example.test/updated-only" || items[1].Summary != "Body only" {
		t.Errorf("entry 1 = %+v", items[1])
	}
	if want := time.Date(2026, 11, 5, 6, 0, 0, 0, time.UTC); !items[1].Published.Equal(want) {
		t.Errorf("updated fallback = %v", items[1].Published)
	}
}

func TestParseFeedLatin1(t *testing.T) {
	body := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><rss><channel><item><title>Atenci\xf3n ni\xf1o</title><link>https://example.test/x</link></item></channel></rss>"
	items, err := parseFeed(strings.NewReader(body))
	if err != nil || len(items) != 1 || items[0].Title != "Atención niño" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
}

func TestParseFeedRejectsNonFeedAndOversize(t *testing.T) {
	if _, err := parseFeed(strings.NewReader("<html><body>hi</body></html>")); err == nil {
		t.Error("an HTML page must not parse as a feed")
	}
	if _, err := parseFeed(strings.NewReader("not xml at all")); err == nil {
		t.Error("garbage must not parse as a feed")
	}
	big := "<rss><channel><item><title>x</title><link>https://example.test/x</link><description>" + strings.Repeat("a", maxFeedBodyBytes) + "</description></item></channel></rss>"
	_, err := parseFeed(strings.NewReader(big))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("a body over the cap must fail with 'exceeds', got %v", err)
	}
}

func TestParseFeedCapsItemsAndText(t *testing.T) {
	var b strings.Builder
	b.WriteString("<rss><channel>")
	for i := 0; i < maxFeedItems+20; i++ {
		b.WriteString("<item><title>t</title><link>https://example.test/" + strings.Repeat("a", 1) + "</link></item>")
	}
	b.WriteString("<item><title>" + strings.Repeat("é", 1000) + "</title><link>https://example.test/long</link><description>" + strings.Repeat("ñ", 1000) + "</description></item>")
	b.WriteString("</channel></rss>")
	items, err := parseFeed(strings.NewReader(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != maxFeedItems {
		t.Fatalf("items = %d, want cap %d", len(items), maxFeedItems)
	}
	long := sanitizeFeedText(strings.Repeat("é", 1000), maxFeedTitleRunes)
	if !utf8.ValidString(long) || utf8.RuneCountInString(long) > maxFeedTitleRunes+3 {
		t.Errorf("length cap broken: %d runes", utf8.RuneCountInString(long))
	}
}

func TestSanitizeFeedText(t *testing.T) {
	cases := map[string]string{
		"<b>bold</b> text":                          "bold text",
		"a &lt;script&gt;x()&lt;/script&gt; b":      "a b",
		"&amp;lt;i&amp;gt;double&amp;lt;/i&amp;gt;": "double",
		"line1\r\nline2\u2028line3\t\tend":          "line1 line2 line3 end",
		"5 < 6 and 7 > 3":                           "5 < 6 and 7 > 3",
		"<unclosed tag":                             "",
		"ctl\x00\x07chars":                          "ctlchars",
		"<style>p{}</style>kept":                    "kept",
	}
	for in, want := range cases {
		if got := sanitizeFeedText(in, 300); got != want {
			t.Errorf("sanitizeFeedText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchFeedSendsUserAgentAndChecksStatus(t *testing.T) {
	var gotUA, gotAccept string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA, gotAccept = r.Header.Get("User-Agent"), r.Header.Get("Accept")
		_, _ = w.Write([]byte(rssFixture))
	}))
	defer ok.Close()
	items, err := fetchFeed(context.Background(), http.DefaultClient, ok.URL)
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%d err=%v", len(items), err)
	}
	if !strings.Contains(gotUA, "SourceRudder") || !strings.Contains(gotUA, "Mozilla/5.0") {
		t.Errorf("User-Agent should extend the fetch default and name SourceRudder: %q", gotUA)
	}
	if !strings.Contains(gotAccept, "xml") {
		t.Errorf("Accept = %q", gotAccept)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "nope", 503) }))
	defer bad.Close()
	if _, err := fetchFeed(context.Background(), http.DefaultClient, bad.URL); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("HTTP 503 must surface as an error naming the status, got %v", err)
	}

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fetchFeed(ctx, http.DefaultClient, slow.URL); err == nil {
		t.Error("a hung server must fail at the context deadline")
	}
}

func TestGoogleNewsSearchURL(t *testing.T) {
	cases := []struct {
		base, query, lang, tr string
		hl, gl, ceid, q       string
	}{
		{"https://gn.test/rss/search", "kubernetes", "es", "", "es-419", "AR", "AR:es-419", "kubernetes"},
		{"https://gn.test/rss/search", "kubernetes", "es-ES", "week", "es-419", "AR", "AR:es-419", "kubernetes when:7d"},
		{"https://gn.test/rss/search", "kubernetes", "en", "day", "en-US", "US", "US:en", "kubernetes when:1d"},
		{"https://gn.test/rss/search", "kubernetes", "", "month", "en-US", "US", "US:en", "kubernetes when:30d"},
		{"https://gn.test/rss/search", "kubernetes", "fr", "year", "en-US", "US", "US:en", "kubernetes when:1y"},
		{"https://gn.test/rss/search", "kubernetes", "en", "bogus", "en-US", "US", "US:en", "kubernetes"},
	}
	for _, c := range cases {
		u := googleNewsSearchURL(c.base, c.query, c.lang, c.tr)
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		q := parsed.Query()
		if q.Get("q") != c.q || q.Get("hl") != c.hl || q.Get("gl") != c.gl || q.Get("ceid") != c.ceid {
			t.Errorf("%+v -> %s", c, u)
		}
	}
}
