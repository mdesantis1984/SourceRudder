// Package compact renders a search response as compact, line-oriented text.
//
// It is a pure formatter: no I/O, no dependencies beyond pkg/types. The output
// is meant to cost fewer tokens than the JSON wire contract, which stays the
// default. Results are never truncated or dropped; only whitespace is
// collapsed and the field separator is escaped so each result is one line.
package compact

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// Detail selects how much of each result is rendered.
type Detail string

const (
	// Titles renders "<n>. <title> | <url>".
	Titles Detail = "titles"
	// Snippets renders "<n>. <title> | <url> | <snippet>". It is the default.
	Snippets Detail = "snippets"
	// Full renders Snippets plus the non-empty optional fields as key=value.
	Full Detail = "full"
)

// ParseDetail converts a caller-supplied value into a Detail. The empty
// string selects Snippets; an unknown value returns an error naming the
// allowed values.
func ParseDetail(s string) (Detail, error) {
	switch Detail(s) {
	case "":
		return Snippets, nil
	case Titles, Snippets, Full:
		return Detail(s), nil
	}
	return "", fmt.Errorf("invalid detail %q: allowed values are %q, %q, %q", s, Titles, Snippets, Full)
}

// Page carries the paging metadata of a response. It is decoupled from
// pkg/types so the formatter does not depend on how the wire contract
// exposes these fields.
type Page struct {
	ResultSetID string
	Total       int
	// Offset is the index of the first result in the stored set. It only
	// numbers the lines; it is not printed in the header.
	Offset int
	// NextCursor is shown in the header when more results remain.
	NextCursor string
}

// Format renders resp as compact text without a trailing newline. An unknown
// or empty Detail renders as Snippets. The output is deterministic.
func Format(resp types.SearchResponse, page Page, d Detail) string {
	if d != Titles && d != Full {
		d = Snippets
	}

	var out []string
	out = append(out, header(resp, page))

	if len(resp.Results) == 0 {
		out = append(out, "no results")
	}
	for i, r := range resp.Results {
		out = append(out, resultLine(page.Offset+i+1, r, d))
	}

	if s := clean(resp.Summary); s != "" {
		out = append(out, "summary: "+s)
	}
	if d == Full {
		if len(resp.KeyFindings) > 0 {
			out = append(out, "keyFindings:")
			for _, f := range resp.KeyFindings {
				out = append(out, "- "+clean(f))
			}
		}
		if len(resp.SourcesUsed) > 0 {
			src := make([]string, len(resp.SourcesUsed))
			for i, s := range resp.SourcesUsed {
				src[i] = clean(s)
			}
			out = append(out, "sourcesUsed: "+strings.Join(src, ", "))
		}
	}
	out = append(out, extraLines(resp)...)
	for _, w := range resp.Warnings {
		out = append(out, "warning: "+clean(w))
	}
	for _, e := range resp.Errors {
		out = append(out, "error: "+clean(e))
	}
	return strings.Join(out, "\n")
}

// extraLines renders the search-level extras SearXNG returned (answers,
// infoboxes, suggestions, corrections), one labeled line each, in every
// detail level. Blank entries and empty groups render nothing.
func extraLines(resp types.SearchResponse) []string {
	var out []string
	for _, a := range resp.Answers {
		if a = clean(a); a != "" {
			out = append(out, "answer: "+a)
		}
	}
	for _, b := range resp.Infoboxes {
		var parts []string
		for _, p := range []string{b.Title, b.Content, b.URL} {
			if p = clean(p); p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			out = append(out, "infobox: "+strings.Join(parts, " | "))
		}
	}
	if s := joinClean(resp.Suggestions); s != "" {
		out = append(out, "suggestions: "+s)
	}
	if s := joinClean(resp.Corrections); s != "" {
		out = append(out, "corrections: "+s)
	}
	return out
}

func joinClean(in []string) string {
	parts := make([]string, 0, len(in))
	for _, s := range in {
		if s = clean(s); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

func header(resp types.SearchResponse, page Page) string {
	parts := []string{"query: " + clean(resp.Query)}
	if page.ResultSetID != "" {
		parts = append(parts, "resultSetId="+clean(page.ResultSetID))
	}
	// Total is shown when a result set is addressable or when the value
	// carries information; nextCursor only when more results remain.
	if page.ResultSetID != "" || page.Total > 0 {
		parts = append(parts, "total="+strconv.Itoa(page.Total))
	}
	if page.NextCursor != "" {
		parts = append(parts, "nextCursor="+clean(page.NextCursor))
	}
	if resp.Cached {
		parts = append(parts, "cached=true")
	}
	if resp.Partial {
		parts = append(parts, "partial=true")
	}
	return strings.Join(parts, " | ")
}

func resultLine(n int, r types.SearchResultItem, d Detail) string {
	parts := []string{strconv.Itoa(n) + ". " + clean(r.Title), clean(r.URL)}
	if d == Titles {
		return strings.Join(parts, " | ")
	}
	if s := clean(r.Snippet); s != "" {
		parts = append(parts, s)
	}
	if d == Full {
		kv := func(k, v string) {
			if v = clean(v); v != "" {
				parts = append(parts, k+"="+v)
			}
		}
		kv("source", r.Source)
		kv("engine", r.Engine)
		kv("type", r.Type)
		if r.PublishedAt != nil && !r.PublishedAt.IsZero() {
			kv("publishedAt", r.PublishedAt.UTC().Format(time.RFC3339))
		}
		kv("author", r.Author)
		if len(r.Tags) > 0 {
			tags := make([]string, 0, len(r.Tags))
			for _, t := range r.Tags {
				if t = clean(t); t != "" {
					tags = append(tags, t)
				}
			}
			kv("tags", strings.Join(tags, ","))
		}
		kv("citationId", r.CitationID)
		if r.CanonicalURL != "" && r.CanonicalURL != r.URL {
			kv("canonicalUrl", r.CanonicalURL)
		}
	}
	return strings.Join(parts, " | ")
}

// clean collapses every run of whitespace (including newlines) into a single
// space, trims the ends and escapes the field separator.
func clean(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", `\|`)
}
