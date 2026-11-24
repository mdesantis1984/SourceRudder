package connectors

import (
	"strings"
	"unicode/utf8"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// maxSearxngExtraRunes caps every string taken from the SearXNG answers,
// infoboxes, suggestions and corrections fields.
const maxSearxngExtraRunes = 500

// searxngExtras are the search-level fields of a SearXNG page 1 response,
// already sanitized.
type searxngExtras struct {
	answers     []string
	infoboxes   []types.Infobox
	suggestions []string
	corrections []string
}

// rawSearxngExtras holds the fields as SearXNG sent them. They are decoded as
// interface{} so an unexpected shape drops the field, never the whole page.
type rawSearxngExtras struct {
	Answers     interface{} `json:"answers"`
	Infoboxes   interface{} `json:"infoboxes"`
	Suggestions interface{} `json:"suggestions"`
	Corrections interface{} `json:"corrections"`
}

func (r rawSearxngExtras) sanitize() *searxngExtras {
	e := &searxngExtras{
		answers:     sanitizeAnswers(r.Answers),
		infoboxes:   sanitizeInfoboxes(r.Infoboxes),
		suggestions: sanitizeStringList(r.Suggestions),
		corrections: sanitizeStringList(r.Corrections),
	}
	if len(e.answers) == 0 && len(e.infoboxes) == 0 && len(e.suggestions) == 0 && len(e.corrections) == 0 {
		return nil
	}
	return e
}

// apply copies the extras into resp. A nil receiver leaves resp untouched.
func (e *searxngExtras) apply(resp *types.SearchResponse) {
	if e == nil || resp == nil {
		return
	}
	resp.Answers = e.answers
	resp.Infoboxes = e.infoboxes
	resp.Suggestions = e.suggestions
	resp.Corrections = e.corrections
}

// sanitizeSearxngText turns an upstream string into one trimmed line: CR, LF,
// U+2028 and U+2029 become spaces, whitespace runs collapse, and the result is
// cut to maxSearxngExtraRunes runes (mcp-searxng#270: answers may carry
// newlines that break line-oriented clients).
func sanitizeSearxngText(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "\u2028", " ", "\u2029", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > maxSearxngExtraRunes {
		s = strings.TrimSpace(string([]rune(s)[:maxSearxngExtraRunes]))
	}
	return s
}

// addUnique appends the sanitized text unless it is empty or already present.
func addUnique(out []string, seen map[string]struct{}, raw string) []string {
	s := sanitizeSearxngText(raw)
	if s == "" {
		return out
	}
	if _, dup := seen[s]; dup {
		return out
	}
	seen[s] = struct{}{}
	return append(out, s)
}

// sanitizeAnswers accepts entries that are a string or an object with a string
// `answer` field; every other entry is skipped.
func sanitizeAnswers(v interface{}) []string {
	list, _ := v.([]interface{})
	var out []string
	seen := map[string]struct{}{}
	for _, entry := range list {
		switch a := entry.(type) {
		case string:
			out = addUnique(out, seen, a)
		case map[string]interface{}:
			if text, ok := a["answer"].(string); ok {
				out = addUnique(out, seen, text)
			}
		}
	}
	return out
}

// sanitizeStringList keeps the string entries of a list (suggestions,
// corrections).
func sanitizeStringList(v interface{}) []string {
	list, _ := v.([]interface{})
	var out []string
	seen := map[string]struct{}{}
	for _, entry := range list {
		if s, ok := entry.(string); ok {
			out = addUnique(out, seen, s)
		}
	}
	return out
}

// sanitizeInfoboxes keeps object entries that have a title or content. The URL
// is the infobox id when it is an http(s) URL, else the first http(s) URL of
// `urls`.
func sanitizeInfoboxes(v interface{}) []types.Infobox {
	list, _ := v.([]interface{})
	var out []types.Infobox
	seen := map[types.Infobox]struct{}{}
	for _, entry := range list {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		box := types.Infobox{}
		if s, ok := m["infobox"].(string); ok {
			box.Title = sanitizeSearxngText(s)
		}
		if s, ok := m["content"].(string); ok {
			box.Content = sanitizeSearxngText(s)
		}
		if box.Title == "" && box.Content == "" {
			continue
		}
		box.URL = infoboxURL(m)
		if _, dup := seen[box]; dup {
			continue
		}
		seen[box] = struct{}{}
		out = append(out, box)
	}
	return out
}

func infoboxURL(m map[string]interface{}) string {
	if id, ok := m["id"].(string); ok {
		if u := sanitizeSearxngText(id); isHTTPURL(u) {
			return u
		}
	}
	urls, _ := m["urls"].([]interface{})
	for _, entry := range urls {
		obj, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if raw, ok := obj["url"].(string); ok {
			if u := sanitizeSearxngText(raw); isHTTPURL(u) {
				return u
			}
		}
	}
	return ""
}
