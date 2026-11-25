package connectors

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mdesantis1984/SourceRudder/internal/fetch"
)

// Shared RSS 2.0 / Atom reader used by search_news (Google News RSS) and
// search_tech_news (Google News RSS plus the curated feeds). Everything in a
// feed is untrusted: the body is size capped, the item count is capped, links
// must be http(s), and every text field is stripped of markup, entities,
// control characters and extra whitespace before it leaves this file.

const (
	// maxFeedBodyBytes caps the bytes read from a feed.
	maxFeedBodyBytes = 2 << 20
	// maxFeedItems caps the items kept per feed.
	maxFeedItems        = 100
	maxFeedTitleRunes   = 300
	maxFeedSnippetRunes = 300
	maxFeedNameRunes    = 120
	maxFeedLinkBytes    = 2048

	// googleNewsRSSURL is the keyless Google News RSS search endpoint.
	googleNewsRSSURL = "https://news.google.com/rss/search"
	// googleNewsEngine names Google News RSS results in Engine.
	googleNewsEngine = "google news rss"
)

// feedUserAgent extends the fetch engine's default User-Agent (many sites
// reject self-identified bot agents) with a token that names this client.
var feedUserAgent = fetch.DefaultUserAgent + " SourceRudder/3.0.0 (+https://github.com/mdesantis1984/SourceRudder)"

// feedItem is one sanitized RSS item or Atom entry. It is also the unit the
// tech news connector caches, so it round-trips through JSON.
type feedItem struct {
	Title      string    `json:"title"`
	Link       string    `json:"link"`
	Summary    string    `json:"summary,omitempty"`
	Author     string    `json:"author,omitempty"`
	Published  time.Time `json:"published"`
	SourceName string    `json:"sourceName,omitempty"`
	SourceURL  string    `json:"sourceUrl,omitempty"`
}

type rssDoc struct {
	Channel struct {
		Items []rssItem `xml:"item"`
	} `xml:"channel"`
}

type rssItem struct {
	Title       string   `xml:"title"`
	Links       []string `xml:"link"`
	Description string   `xml:"description"`
	Encoded     string   `xml:"encoded"`
	PubDate     string   `xml:"pubDate"`
	DCDate      string   `xml:"date"`
	Creator     string   `xml:"creator"`
	Author      string   `xml:"author"`
	Source      struct {
		URL  string `xml:"url,attr"`
		Name string `xml:",chardata"`
	} `xml:"source"`
}

type atomDoc struct {
	Entries []atomEntry `xml:"entry"`
}

type atomEntry struct {
	Title string `xml:"title"`
	Links []struct {
		Rel  string `xml:"rel,attr"`
		Href string `xml:"href,attr"`
	} `xml:"link"`
	Summary   string `xml:"summary"`
	Content   string `xml:"content"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
	Author    struct {
		Name string `xml:"name"`
	} `xml:"author"`
}

// parseFeed reads an RSS 2.0 or Atom document of at most maxFeedBodyBytes and
// returns its valid items (http(s) link, non-empty title), at most
// maxFeedItems.
func parseFeed(r io.Reader) ([]feedItem, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxFeedBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read feed: %w", err)
	}
	if len(data) > maxFeedBodyBytes {
		return nil, fmt.Errorf("feed body exceeds %d bytes", maxFeedBodyBytes)
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = feedCharsetReader
	for {
		tok, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("not an RSS or Atom feed")
			}
			return nil, fmt.Errorf("parse feed: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch start.Name.Local {
		case "rss":
			var doc rssDoc
			if err := dec.DecodeElement(&doc, &start); err != nil {
				return nil, fmt.Errorf("parse rss: %w", err)
			}
			return rssItems(doc.Channel.Items), nil
		case "feed":
			var doc atomDoc
			if err := dec.DecodeElement(&doc, &start); err != nil {
				return nil, fmt.Errorf("parse atom: %w", err)
			}
			return atomItems(doc.Entries), nil
		default:
			return nil, errors.New("not an RSS or Atom feed")
		}
	}
}

// feedCharsetReader decodes the single-byte Latin charsets feeds still
// declare; any other label is read as UTF-8.
func feedCharsetReader(label string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "l1", "windows-1252", "cp1252":
		raw, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		runes := make([]rune, len(raw))
		for i, b := range raw {
			runes[i] = rune(b)
		}
		return strings.NewReader(string(runes)), nil
	}
	return in, nil
}

func rssItems(in []rssItem) []feedItem {
	out := make([]feedItem, 0, len(in))
	for _, it := range in {
		if len(out) >= maxFeedItems {
			break
		}
		link := ""
		for _, l := range it.Links {
			if l = strings.TrimSpace(l); l != "" {
				link = l
				break
			}
		}
		desc := it.Description
		if strings.TrimSpace(desc) == "" {
			desc = it.Encoded
		}
		author := it.Creator
		if strings.TrimSpace(author) == "" {
			author = it.Author
		}
		item, ok := newFeedItem(it.Title, link, desc, author, firstNonEmpty(it.PubDate, it.DCDate))
		if !ok {
			continue
		}
		item.SourceName = sanitizeFeedText(it.Source.Name, maxFeedNameRunes)
		if u := strings.TrimSpace(it.Source.URL); len(u) <= maxFeedLinkBytes && isHTTPURL(u) {
			item.SourceURL = u
		}
		out = append(out, item)
	}
	return out
}

func atomItems(in []atomEntry) []feedItem {
	out := make([]feedItem, 0, len(in))
	for _, e := range in {
		if len(out) >= maxFeedItems {
			break
		}
		link := ""
		for _, l := range e.Links {
			href := strings.TrimSpace(l.Href)
			if href == "" {
				continue
			}
			if l.Rel == "" || l.Rel == "alternate" {
				link = href
				break
			}
			if link == "" && l.Rel != "self" && l.Rel != "edit" {
				link = href
			}
		}
		desc := e.Summary
		if strings.TrimSpace(desc) == "" {
			desc = e.Content
		}
		item, ok := newFeedItem(e.Title, link, desc, e.Author.Name, firstNonEmpty(e.Published, e.Updated))
		if !ok {
			continue
		}
		out = append(out, item)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// newFeedItem sanitizes the raw fields; it reports false for an item without
// a title or without an http(s) link.
func newFeedItem(title, link, summary, author, date string) (feedItem, bool) {
	link = strings.TrimSpace(link)
	if len(link) > maxFeedLinkBytes || !isHTTPURL(link) {
		return feedItem{}, false
	}
	t := sanitizeFeedText(title, maxFeedTitleRunes)
	if t == "" {
		return feedItem{}, false
	}
	return feedItem{
		Title:     t,
		Link:      link,
		Summary:   sanitizeFeedText(summary, maxFeedSnippetRunes),
		Author:    sanitizeFeedText(author, maxFeedNameRunes),
		Published: parseFeedDate(date),
	}, true
}

var feedDateLayouts = []string{
	time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822,
	"Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "2 Jan 2006 15:04:05 -0700",
	time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
}

// parseFeedDate returns the zero time when the value is empty or unknown.
func parseFeedDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range feedDateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// blockTags become a space when stripped so adjacent blocks do not glue words.
var blockTags = map[string]bool{
	"p": true, "br": true, "div": true, "li": true, "ul": true, "ol": true, "tr": true, "td": true,
	"th": true, "table": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"blockquote": true, "pre": true, "hr": true, "section": true, "article": true,
}

// stripHTMLTags removes tags, comments and the content of script/style
// elements. A '<' that does not start a tag ("5 < 6") stays; a tag that never
// closes drops the rest of the text.
func stripHTMLTags(s string) string {
	if !strings.Contains(s, "<") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '<' || i+1 >= len(s) || !isTagStart(s[i+1]) {
			b.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			break
		}
		tag := s[i+1 : i+end]
		i += end + 1
		name := strings.ToLower(strings.TrimLeft(tag, "/"))
		if cut := strings.IndexAny(name, " \t\r\n/>"); cut >= 0 {
			name = name[:cut]
		}
		if (name == "script" || name == "style") && !strings.HasPrefix(tag, "/") && !strings.HasSuffix(tag, "/") {
			closeAt := strings.Index(strings.ToLower(s[i:]), "</"+name)
			if closeAt < 0 {
				break
			}
			i += closeAt
			if gt := strings.IndexByte(s[i:], '>'); gt >= 0 {
				i += gt + 1
			} else {
				break
			}
		}
		if blockTags[name] {
			b.WriteByte(' ')
		}
	}
	return b.String()
}

func isTagStart(c byte) bool {
	return c == '/' || c == '!' || c == '?' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// sanitizeFeedText turns untrusted feed markup into one plain line: tags and
// entities (even doubly escaped) are removed, control and invisible format
// characters dropped, whitespace collapsed, and the result cut to maxRunes
// runes with an ellipsis.
func sanitizeFeedText(s string, maxRunes int) string {
	for i := 0; i < 3; i++ {
		next := html.UnescapeString(stripHTMLTags(s))
		if next == s {
			break
		}
		s = next
	}
	s = stripHTMLTags(s)
	s = strings.Map(func(r rune) rune {
		if r == utf8.RuneError || (unicode.IsControl(r) && !unicode.IsSpace(r)) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
	s = sanitizeSearxngText(s)
	if maxRunes > 0 && utf8.RuneCountInString(s) > maxRunes {
		s = strings.TrimSpace(string([]rune(s)[:maxRunes])) + "..."
	}
	return s
}

// fetchFeed GETs a feed and parses it. The caller controls the deadline with
// ctx and client.Timeout.
func fetchFeed(ctx context.Context, client *http.Client, feedURL string) ([]feedItem, error) {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", feedUserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("feed request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("feed HTTP %d", resp.StatusCode)
	}
	return parseFeed(resp.Body)
}

// feedLocale maps a request language onto Google News hl, gl and ceid: Spanish
// uses es-419 / AR, everything else en-US / US.
func feedLocale(language string) (hl, gl, ceid string) {
	lang := strings.ToLower(strings.TrimSpace(language))
	if cut := strings.IndexAny(lang, "-_"); cut >= 0 {
		lang = lang[:cut]
	}
	if lang == "es" {
		return "es-419", "AR", "AR:es-419"
	}
	return "en-US", "US", "US:en"
}

// googleNewsWhen maps a timeRange to the Google News `when:` operator.
func googleNewsWhen(timeRange string) string {
	switch timeRange {
	case "day":
		return "when:1d"
	case "week":
		return "when:7d"
	case "month":
		// `when:1m` returns nothing on Google News (probed live); 30d works.
		return "when:30d"
	case "year":
		return "when:1y"
	}
	return ""
}

// googleNewsSearchURL builds the RSS search URL; timeRange travels inside the
// query as the `when:` operator.
func googleNewsSearchURL(base, query, language, timeRange string) string {
	hl, gl, ceid := feedLocale(language)
	q := strings.TrimSpace(query)
	if when := googleNewsWhen(timeRange); when != "" {
		q += " " + when
	}
	params := url.Values{}
	params.Set("q", q)
	params.Set("hl", hl)
	params.Set("gl", gl)
	params.Set("ceid", ceid)
	return base + "?" + params.Encode()
}

// accentFolder removes the diacritics of the Latin letters
// used by English and Spanish text (apply it to lower-case text).
var accentFolder = strings.NewReplacer(
	"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "å", "a",
	"é", "e", "è", "e", "ê", "e", "ë", "e",
	"í", "i", "ì", "i", "î", "i", "ï", "i",
	"ó", "o", "ò", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o",
	"ú", "u", "ù", "u", "û", "u", "ü", "u",
	"ñ", "n", "ç", "c", "ý", "y", "ÿ", "y", "ß", "ss", "æ", "ae", "œ", "oe",
)

func foldAccents(s string) string { return accentFolder.Replace(strings.ToLower(s)) }

// feedTokens splits s into lower-case, accent-folded alphanumeric words.
func feedTokens(s string) []string {
	return strings.FieldsFunc(foldAccents(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// feedTitleKey is the deduplication key of a title: accent-folded words, with
// a trailing " - Publisher" removed when the publisher is known.
func feedTitleKey(title, publisher string) string {
	if publisher != "" {
		if suffix := " - " + publisher; strings.HasSuffix(strings.ToLower(title), strings.ToLower(suffix)) {
			title = title[:len(title)-len(suffix)]
		}
	}
	return strings.Join(feedTokens(title), " ")
}

// feedURLKey is the deduplication key of a link: lower-case host without
// "www.", no fragment, no trailing slash, no utm_* parameters. Unlike
// normalization.NormalizeURL it keeps the query, so item?id=1 and item?id=2
// stay distinct.
func feedURLKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.ToLower(strings.TrimSpace(raw))
	}
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(strings.ToLower(k), "utm_") {
			q.Del(k)
		}
	}
	key := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.") + strings.TrimSuffix(u.EscapedPath(), "/")
	if enc := q.Encode(); enc != "" {
		key += "?" + enc
	}
	return key
}

// feedDedupe drops items whose URL key or title key was already seen.
type feedDedupe struct {
	urls   map[string]struct{}
	titles map[string]struct{}
}

func newFeedDedupe() *feedDedupe {
	return &feedDedupe{urls: map[string]struct{}{}, titles: map[string]struct{}{}}
}

// add registers the item and reports false when it duplicates an earlier one.
func (d *feedDedupe) add(link, title, publisher string) bool {
	uk, tk := feedURLKey(link), feedTitleKey(title, publisher)
	if _, dup := d.urls[uk]; dup {
		return false
	}
	if tk != "" {
		if _, dup := d.titles[tk]; dup {
			return false
		}
		d.titles[tk] = struct{}{}
	}
	d.urls[uk] = struct{}{}
	return true
}

// seed registers an item without testing it (results already in the list).
func (d *feedDedupe) seed(link, title, publisher string) { d.add(link, title, publisher) }
