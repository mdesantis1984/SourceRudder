package fetch

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type Extractor struct{}

func NewExtractor() *Extractor {
	return &Extractor{}
}

func (e *Extractor) Extract(ctx context.Context, html string, urlStr string) (*types.FetchResponse, error) {
	title := e.ExtractTitle(html)
	content, err := e.ExtractMainContent(html)
	if err != nil {
		content = ""
	}
	metadata, _ := e.ExtractMetadata(html)

	return &types.FetchResponse{
		URL:      urlStr,
		Title:    title,
		Content:  content,
		Metadata: metadata,
	}, nil
}

func (e *Extractor) ExtractTitle(html string) string {
	titlePatterns := []struct {
		pattern *regexp.Regexp
		group   int
	}{
		{regexp.MustCompile(`<meta[^>]*property=["']og:title["'][^>]*content=["']([^"']*)["']`), 1},
		{regexp.MustCompile(`<meta[^>]*content=["']([^"']*)["'][^>]*property=["']og:title["']`), 1},
		{regexp.MustCompile(`<title[^>]*>([^<]*)</title>`), 1},
		{regexp.MustCompile(`<h1[^>]*>([^<]*)</h1>`), 1},
	}

	for _, tp := range titlePatterns {
		if match := tp.pattern.FindStringSubmatch(html); len(match) > tp.group {
			return strings.TrimSpace(match[tp.group])
		}
	}

	return ""
}

var scriptRegex = regexp.MustCompile(`<script[^>]*>[\s\S]*?</script>`)
var styleRegex = regexp.MustCompile(`<style[^>]*>[\s\S]*?</style>`)
var commentRegex = regexp.MustCompile(`<!--[\s\S]*?-->`)
var tagRegex = regexp.MustCompile(`<[^>]+>`)
var whitespaceRegex = regexp.MustCompile(`\s+`)

func cleanHTML(html string) string {
	html = scriptRegex.ReplaceAllString(html, "")
	html = styleRegex.ReplaceAllString(html, "")
	html = commentRegex.ReplaceAllString(html, "")
	return html
}

func (e *Extractor) ExtractMainContent(html string) (string, error) {
	cleaned := cleanHTML(html)

	articlePatterns := []string{
		`<article[^>]*>([\s\S]*?)</article>`,
		`<main[^>]*>([\s\S]*?)</main>`,
		`<div[^>]*class=["'][^"']*(?:content|article|post|entry|main|body)[^"']*["'][^>]*>([\s\S]*?)</div>`,
	}

	for _, pattern := range articlePatterns {
		re := regexp.MustCompile(pattern)
		match := re.FindStringSubmatch(cleaned)
		if len(match) > 1 {
			text := tagRegex.ReplaceAllString(match[1], "\n")
			text = whitespaceRegex.ReplaceAllString(text, " ")
			text = strings.TrimSpace(text)
			if len(text) > 100 {
				return text, nil
			}
		}
	}

	paragraphs := regexp.MustCompile(`<p[^>]*>([\s\S]*?)</p>`)
	matches := paragraphs.FindAllStringSubmatch(cleaned, -1)

	if len(matches) == 0 {
		text := tagRegex.ReplaceAllString(cleaned, " ")
		text = whitespaceRegex.ReplaceAllString(text, " ")
		return strings.TrimSpace(text), nil
	}

	var bestContent strings.Builder
	for _, match := range matches {
		text := tagRegex.ReplaceAllString(match[1], " ")
		text = whitespaceRegex.ReplaceAllString(text, " ")
		text = strings.TrimSpace(text)
		if len(text) > 50 {
			bestContent.WriteString(text)
			bestContent.WriteString("\n\n")
		}
	}

	content := strings.TrimSpace(bestContent.String())
	if content == "" {
		text := tagRegex.ReplaceAllString(cleaned, " ")
		text = whitespaceRegex.ReplaceAllString(text, " ")
		return strings.TrimSpace(text), nil
	}

	return content, nil
}

func (e *Extractor) ExtractArticleContent(html string) (string, error) {
	cleaned := cleanHTML(html)

	re := regexp.MustCompile(`<article[^>]*>([\s\S]*?)</article>`)
	match := re.FindStringSubmatch(cleaned)
	if len(match) > 1 {
		text := tagRegex.ReplaceAllString(match[1], "\n")
		text = whitespaceRegex.ReplaceAllString(text, " ")
		return strings.TrimSpace(text), nil
	}

	hTags := regexp.MustCompile(`<h[1-6][^>]*>([\s\S]*?)</h[1-6]>`)
	pTags := regexp.MustCompile(`<p[^>]*>([\s\S]*?)</p>`)

	var content strings.Builder
	hMatches := hTags.FindAllStringSubmatch(cleaned, -1)
	for _, m := range hMatches {
		text := tagRegex.ReplaceAllString(m[1], " ")
		text = strings.TrimSpace(text)
		if text != "" {
			content.WriteString("## ")
			content.WriteString(text)
			content.WriteString("\n\n")
		}
	}

	pMatches := pTags.FindAllStringSubmatch(cleaned, -1)
	for _, m := range pMatches {
		text := tagRegex.ReplaceAllString(m[1], " ")
		text = whitespaceRegex.ReplaceAllString(text, " ")
		text = strings.TrimSpace(text)
		if len(text) > 30 {
			content.WriteString(text)
			content.WriteString("\n\n")
		}
	}

	return strings.TrimSpace(content.String()), nil
}

func (e *Extractor) ExtractDocContent(html string) (string, error) {
	cleaned := cleanHTML(html)

	codeBlocks := regexp.MustCompile(`<pre[^>]*>([\s\S]*?)</pre>`)
	matches := codeBlocks.FindAllStringSubmatch(cleaned, -1)

	var content strings.Builder
	for _, match := range matches {
		text := tagRegex.ReplaceAllString(match[1], " ")
		text = whitespaceRegex.ReplaceAllString(text, " ")
		text = strings.TrimSpace(text)
		if text != "" {
			content.WriteString("```\n")
			content.WriteString(text)
			content.WriteString("\n```\n\n")
		}
	}

	headers := regexp.MustCompile(`<h[1-4][^>]*>([\s\S]*?)</h[1-4]>`)
	hMatches := headers.FindAllStringSubmatch(cleaned, -1)
	for _, m := range hMatches {
		text := tagRegex.ReplaceAllString(m[1], " ")
		text = strings.TrimSpace(text)
		if text != "" {
			level := "#"
			if strings.HasPrefix(m[0], "<h2") {
				level = "##"
			} else if strings.HasPrefix(m[0], "<h3") {
				level = "###"
			} else if strings.HasPrefix(m[0], "<h4") {
				level = "####"
			}
			content.WriteString(level)
			content.WriteString(" ")
			content.WriteString(text)
			content.WriteString("\n\n")
		}
	}

	lists := regexp.MustCompile(`<[uo]l[^>]*>([\s\S]*?)</[uo]l>`)
	listMatches := lists.FindAllStringSubmatch(cleaned, -1)
	for _, match := range listMatches {
		items := regexp.MustCompile(`<li[^>]*>([\s\S]*?)</li>`)
		itemMatches := items.FindAllStringSubmatch(match[1], -1)
		for _, item := range itemMatches {
			text := tagRegex.ReplaceAllString(item[1], " ")
			text = whitespaceRegex.ReplaceAllString(text, " ")
			text = strings.TrimSpace(text)
			if text != "" {
				content.WriteString("- ")
				content.WriteString(text)
				content.WriteString("\n")
			}
		}
		content.WriteString("\n")
	}

	return strings.TrimSpace(content.String()), nil
}

func (e *Extractor) ExtractTables(html string) ([]map[string]interface{}, error) {
	var tables []map[string]interface{}

	tableRegex := regexp.MustCompile(`<table[^>]*>([\s\S]*?)</table>`)
	tablesMatch := tableRegex.FindAllStringSubmatch(html, -1)

	for _, tableMatch := range tablesMatch {
		tableContent := tableMatch[1]

		headers := regexp.MustCompile(`<th[^>]*>([\s\S]*?)</th>`)
		headerMatches := headers.FindAllStringSubmatch(tableContent, -1)

		var headerCells []string
		for _, h := range headerMatches {
			text := tagRegex.ReplaceAllString(h[1], " ")
			text = whitespaceRegex.ReplaceAllString(text, " ")
			headerCells = append(headerCells, strings.TrimSpace(text))
		}

		rows := regexp.MustCompile(`<tr[^>]*>([\s\S]*?)</tr>`)
		rowMatches := rows.FindAllStringSubmatch(tableContent, -1)

		for _, rowMatch := range rowMatches {
			cells := regexp.MustCompile(`<t[dh][^>]*>([\s\S]*?)</t[dh]>`)
			cellMatches := cells.FindAllStringSubmatch(rowMatch[1], -1)

			if len(cellMatches) == 0 {
				continue
			}

			row := make(map[string]interface{})
			if len(headerCells) > 0 && len(headerCells) == len(cellMatches) {
				for i, cell := range cellMatches {
					text := tagRegex.ReplaceAllString(cell[1], " ")
					text = whitespaceRegex.ReplaceAllString(text, " ")
					row[headerCells[i]] = strings.TrimSpace(text)
				}
			} else {
				for i, cell := range cellMatches {
					text := tagRegex.ReplaceAllString(cell[1], " ")
					text = whitespaceRegex.ReplaceAllString(text, " ")
					row[fmt.Sprintf("column_%d", i)] = strings.TrimSpace(text)
				}
			}

			tables = append(tables, row)
		}
	}

	return tables, nil
}

func (e *Extractor) ExtractMetadata(html string) (map[string]string, error) {
	metadata := make(map[string]string)

	metaPatterns := map[string]*regexp.Regexp{
		"description":      regexp.MustCompile(`<meta[^>]*name=["']description["'][^>]*content=["']([^"']*)["']`),
		"keywords":         regexp.MustCompile(`<meta[^>]*name=["']keywords["'][^>]*content=["']([^"']*)["']`),
		"og:title":         regexp.MustCompile(`<meta[^>]*property=["']og:title["'][^>]*content=["']([^"']*)["']`),
		"og:description":   regexp.MustCompile(`<meta[^>]*property=["']og:description["'][^>]*content=["']([^"']*)["']`),
		"og:image":         regexp.MustCompile(`<meta[^>]*property=["']og:image["'][^>]*content=["']([^"']*)["']`),
		"twitter:card":     regexp.MustCompile(`<meta[^>]*name=["']twitter:card["'][^>]*content=["']([^"']*)["']`),
		"twitter:title":    regexp.MustCompile(`<meta[^>]*name=["']twitter:title["'][^>]*content=["']([^"']*)["']`),
		"author":           regexp.MustCompile(`<meta[^>]*name=["']author["'][^>]*content=["']([^"']*)["']`),
		"robots":           regexp.MustCompile(`<meta[^>]*name=["']robots["'][^>]*content=["']([^"']*)["']`),
		"canonical":        regexp.MustCompile(`<link[^>]*rel=["']canonical["'][^>]*href=["']([^"']*)["']`),
		"alternate":        regexp.MustCompile(`<link[^>]*rel=["']alternate["'][^>]*href=["']([^"']*)["']`),
	}

	for key, pattern := range metaPatterns {
		if match := pattern.FindStringSubmatch(html); len(match) > 1 {
			metadata[key] = strings.TrimSpace(match[1])
		}
	}

	if title := e.ExtractTitle(html); title != "" {
		metadata["title"] = title
	}

	jsonldPattern := regexp.MustCompile(`<script[^>]*type=["']application/ld\+json["'][^>]*>([\s\S]*?)</script>`)
	if match := jsonldPattern.FindStringSubmatch(html); len(match) > 1 {
		var jsonld map[string]interface{}
		if err := json.Unmarshal([]byte(match[1]), &jsonld); err == nil {
			if name, ok := jsonld["name"].(string); ok {
				metadata["jsonld:name"] = name
			}
			if desc, ok := jsonld["description"].(string); ok {
				metadata["jsonld:description"] = desc
			}
			if url, ok := jsonld["url"].(string); ok {
				metadata["jsonld:url"] = url
			}
		}
	}

	return metadata, nil
}
