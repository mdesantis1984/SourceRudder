package connectors

import (
	"context"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/thiscloud/ia-buscar/pkg/types"
)

const maxLocalIndexResults = 50

// LocalIndexConnector searches an immutable, operator-curated corpus loaded at
// startup. It never crawls the filesystem or performs network requests.
type LocalIndexConnector struct {
	documents []localIndexDocument
}

func NewLocalIndexConnector(path string) (*LocalIndexConnector, error) {
	documents, err := loadLocalIndexCorpus(path)
	if err != nil {
		return nil, err
	}
	return &LocalIndexConnector{documents: documents}, nil
}

func (c *LocalIndexConnector) Name() string { return "local_index" }

func (c *LocalIndexConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	terms := localIndexTerms(req.Query)
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = 10
	}
	if maxResults > maxLocalIndexResults {
		maxResults = maxLocalIndexResults
	}

	type rankedDocument struct {
		doc   localIndexDocument
		score float64
	}
	ranked := make([]rankedDocument, 0, len(c.documents))
	phrase := strings.ToLower(strings.TrimSpace(req.Query))
	for _, doc := range c.documents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if score, ok := scoreLocalIndexDocument(doc, terms, phrase); ok {
			ranked = append(ranked, rankedDocument{doc: doc, score: score})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].doc.Title < ranked[j].doc.Title
		}
		return ranked[i].score > ranked[j].score
	})
	if len(ranked) > maxResults {
		ranked = ranked[:maxResults]
	}

	results := make([]types.SearchResultItem, 0, len(ranked))
	for _, item := range ranked {
		results = append(results, types.SearchResultItem{
			Title:      item.doc.Title,
			URL:        item.doc.URL,
			Snippet:    item.doc.Snippet,
			Source:     c.Name(),
			Type:       "document",
			Score:      item.score,
			Author:     item.doc.Author,
			Tags:       append([]string(nil), item.doc.Tags...),
			CitationID: "local-index:" + item.doc.ID,
		})
	}

	return &types.SearchResponse{
		Query:       req.Query,
		Results:     results,
		SourcesUsed: []string{c.Name()},
		Strategy:    "local_index_lexical",
	}, nil
}

func localIndexTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	terms := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if utf8.RuneCountInString(field) > 64 || len(terms) == 32 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
}

func scoreLocalIndexDocument(doc localIndexDocument, terms []string, phrase string) (float64, bool) {
	if len(terms) == 0 {
		return 0, false
	}
	title := strings.ToLower(doc.Title)
	snippet := strings.ToLower(doc.Snippet)
	tags := strings.ToLower(strings.Join(doc.Tags, " "))
	id := strings.ToLower(doc.ID)
	score := 0.0
	for _, term := range terms {
		matched := false
		if strings.Contains(title, term) {
			score += 4
			matched = true
		}
		if strings.Contains(tags, term) {
			score += 3
			matched = true
		}
		if strings.Contains(snippet, term) {
			score++
			matched = true
		}
		if strings.Contains(id, term) {
			score += 2
			matched = true
		}
		if !matched {
			return 0, false
		}
	}
	if strings.Contains(title, phrase) {
		score += 8
	} else if strings.Contains(snippet, phrase) {
		score += 2
	}
	return score, true
}
