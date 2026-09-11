package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type Service struct {
	synthesizer *Synthesizer
}

func NewService() *Service {
	return &Service{
		synthesizer: NewSynthesizer(),
	}
}

func (s *Service) Summarize(ctx context.Context, query string, results []types.SearchResultItem) (*SynthesisResult, error) {
	if len(results) == 0 {
		return &SynthesisResult{
			Summary:     "No results to synthesize.",
			KeyFindings: []string{},
			Citations:   []string{},
			Confidence:  0.0,
		}, nil
	}

	citationIDs := make([]string, 0, len(results))
	for i := range results {
		if results[i].CitationID != "" {
			citationIDs = append(citationIDs, results[i].CitationID)
		} else {
			citationIDs = append(citationIDs, results[i].URL)
		}
	}

	confidence := calculateConfidence(len(results), results)

	useCount := min(3, len(results))
	summaryParts := make([]string, 0, useCount)
	for i := 0; i < useCount; i++ {
		if results[i].Snippet != "" {
			summaryParts = append(summaryParts, results[i].Snippet)
		}
	}
	summary := buildSummary(query, summaryParts)

	keyFindings := extractKeyFindings(query, results)

	return &SynthesisResult{
		Summary:     summary,
		KeyFindings: keyFindings,
		Citations:   citationIDs,
		Confidence:  confidence,
	}, nil
}

func (s *Service) DeepResearch(ctx context.Context, query string, results []types.SearchResultItem) (*DeepResearchResult, error) {
	if len(results) == 0 {
		return &DeepResearchResult{
			Summary:     "No results for deep research.",
			Themes:      []ThemeResults{},
			KeyFindings: []string{},
			Comparison:  map[string]SourceInfo{},
			Confidence:  0.0,
		}, nil
	}

	themes := groupByTheme(results)
	keyFindings := extractKeyFindings(query, results)
	comparison := buildComparisonMap(results)
	confidence := calculateConfidence(len(results), results)

	summary := buildDeepSummary(query, themes)

	return &DeepResearchResult{
		Summary:     summary,
		Themes:      themes,
		KeyFindings: keyFindings,
		Comparison:  comparison,
		Confidence:  confidence,
	}, nil
}

func (s *Service) CompareSources(ctx context.Context, results []types.SearchResultItem, query string) (*CompareResult, error) {
	if len(results) == 0 {
		return &CompareResult{
			Sources:     []SourceComparison{},
			Consensus:   "No sources to compare.",
			Divergences: []string{},
		}, nil
	}

	sources := make([]SourceComparison, 0, len(results))
	seenURLs := make(map[string]bool)

	for i := range results {
		if seenURLs[results[i].URL] {
			continue
		}
		seenURLs[results[i].URL] = true

		comp := SourceComparison{
			URL:         results[i].URL,
			Title:       results[i].Title,
			Source:      results[i].Source,
			Score:       results[i].Score,
			PublishedAt: results[i].PublishedAt,
		}

		if results[i].PublishedAt != nil {
			comp.IsMostRecent = true
			for j := range results {
				if i != j && results[j].PublishedAt != nil && results[j].PublishedAt.After(*results[i].PublishedAt) {
					comp.IsMostRecent = false
					break
				}
			}
		}

		if results[i].Snippet != "" {
			comp.SnippetPreview = results[i].Snippet
			if len(results[i].Snippet) > 100 {
				comp.SnippetPreview = results[i].Snippet[:100] + "..."
			}
		}

		sources = append(sources, comp)
	}

	consensus, divergencies := findConsensusAndDivergences(results, query)

	return &CompareResult{
		Sources:     sources,
		Consensus:   consensus,
		Divergences: divergencies,
	}, nil
}

type Synthesizer struct{}

func NewSynthesizer() *Synthesizer {
	return &Synthesizer{}
}

func (s *Synthesizer) Synthesize(ctx context.Context, data []byte, goal string) ([]byte, error) {
	var results []*types.SearchResultItem
	if err := json.Unmarshal(data, &results); err != nil {
		return nil, err
	}

	svc := &Service{synthesizer: s}
	result, err := svc.Summarize(ctx, goal, convertResults(results))
	if err != nil {
		return nil, err
	}

	return json.Marshal(result)
}

func (s *Synthesizer) ParseResults(raw []byte) ([]*types.SearchResultItem, error) {
	var results []*types.SearchResultItem
	if err := json.Unmarshal(raw, &results); err != nil {
		return nil, err
	}
	return results, nil
}

type SynthesisResult struct {
	Summary     string   `json:"summary"`
	KeyFindings []string `json:"keyFindings"`
	Citations   []string `json:"citations"`
	Confidence  float64  `json:"confidence"`
}

type DeepResearchResult struct {
	Summary     string                `json:"summary"`
	Themes      []ThemeResults        `json:"themes"`
	KeyFindings []string              `json:"keyFindings"`
	Comparison  map[string]SourceInfo `json:"comparison"`
	Confidence  float64               `json:"confidence"`
}

type ThemeResults struct {
	Theme   string                   `json:"theme"`
	Results []types.SearchResultItem  `json:"results"`
	Summary string                   `json:"summary"`
}

type SourceInfo struct {
	URL           string  `json:"url"`
	Title        string  `json:"title"`
	RecencyScore float64 `json:"recencyScore"`
	Completeness float64 `json:"completeness"`
	Reliability  float64 `json:"reliability"`
}

type CompareResult struct {
	Sources     []SourceComparison `json:"sources"`
	Consensus   string             `json:"consensus"`
	Divergences []string           `json:"divergences"`
}

type SourceComparison struct {
	URL            string     `json:"url"`
	Title          string     `json:"title"`
	Source         string     `json:"source"`
	Score          float64    `json:"score"`
	PublishedAt    *time.Time `json:"publishedAt,omitempty"`
	IsMostRecent   bool       `json:"isMostRecent"`
	SnippetPreview string     `json:"snippetPreview,omitempty"`
}

func convertResults(items []*types.SearchResultItem) []types.SearchResultItem {
	result := make([]types.SearchResultItem, len(items))
	for i, item := range items {
		result[i] = *item
	}
	return result
}

func calculateConfidence(count int, results []types.SearchResultItem) float64 {
	if count == 0 {
		return 0.0
	}
	if count > 5 {
		return 0.9
	}
	if count > 3 {
		return 0.75
	}
	if count > 1 {
		return 0.5
	}
	return 0.3
}

func buildSummary(query string, snippets []string) string {
	if len(snippets) == 0 {
		return "Search completed for " + query + "."
	}

	var builder strings.Builder
	builder.WriteString("Based on ")
	builder.WriteString(formatCount(len(snippets)))
	builder.WriteString(" sources, ")
	builder.WriteString(query)
	builder.WriteString(" appears to be related to: ")
	builder.WriteString(strings.Join(snippets, " "))
	return builder.String()
}

func buildDeepSummary(query string, themes []ThemeResults) string {
	if len(themes) == 0 {
		return "No themes identified for " + query
	}
	var builder strings.Builder
	builder.WriteString("Research on ")
	builder.WriteString(query)
	builder.WriteString(" identified ")
	builder.WriteString(formatCount(len(themes)))
	builder.WriteString(" main themes. ")
	for i, theme := range themes {
		if i > 0 {
			builder.WriteString(" ")
		}
		builder.WriteString(theme.Theme)
		builder.WriteString(": ")
		builder.WriteString(theme.Summary)
	}
	return builder.String()
}

func extractKeyFindings(query string, results []types.SearchResultItem) []string {
	if len(results) == 0 {
		return []string{}
	}

	findings := make([]string, 0)
	seenPhrases := make(map[string]bool)

	queryWords := strings.Fields(strings.ToLower(query))

	for _, r := range results {
		if r.Snippet == "" {
			continue
		}
		words := strings.Fields(strings.ToLower(r.Snippet))
		commonWords := 0
		for _, qw := range queryWords {
			for _, sw := range words {
				if qw == sw && len(qw) > 3 {
					commonWords++
					break
				}
			}
		}

		if commonWords >= 2 && len(r.Snippet) > 20 {
			phrase := r.Snippet
			if len(phrase) > 100 {
				phrase = phrase[:100] + "..."
			}
			if !seenPhrases[phrase] {
				seenPhrases[phrase] = true
				findings = append(findings, phrase)
				if len(findings) >= 5 {
					break
				}
			}
		}
	}

	if len(findings) == 0 && len(results) > 0 && results[0].Snippet != "" {
		snippet := results[0].Snippet
		if len(snippet) > 100 {
			snippet = snippet[:100] + "..."
		}
		findings = append(findings, snippet)
	}

	return findings
}

func groupByTheme(results []types.SearchResultItem) []ThemeResults {
	themeMap := make(map[string][]types.SearchResultItem)

	for _, r := range results {
		theme := categorizeByTitle(r.Title, r.Snippet)
		themeMap[theme] = append(themeMap[theme], r)
	}

	themes := make([]ThemeResults, 0, len(themeMap))
	for theme, items := range themeMap {
		summary := buildThemeSummary(items)
		themes = append(themes, ThemeResults{
			Theme:   theme,
			Results: items,
			Summary: summary,
		})
	}

	sort.Slice(themes, func(i, j int) bool {
		return len(themes[i].Results) > len(themes[j].Results)
	})

	return themes
}

func categorizeByTitle(title, snippet string) string {
	if title == "" && snippet == "" {
		return "General"
	}

	text := strings.ToLower(title + " " + snippet)

	categories := map[string][]string{
		"Technical Documentation": {"docs", "documentation", "api", "reference", "guide", "tutorial", "manual"},
		"Code & Programming":     {"github", "code", "source", "repository", "commit", "pull", "issue", "stackoverflow"},
		"News & Updates":          {"news", "announcement", "release", "update", "version", "2024", "2025", "2026"},
		"Packages & Dependencies": {"npm", "pypi", "nuget", "docker", "hub", "package", "library", "framework"},
		"Community & Discussion":  {"community", "forum", "discussion", "blog", "article", "post"},
	}

	for category, keywords := range categories {
		for _, kw := range keywords {
			if strings.Contains(text, kw) {
				return category
			}
		}
	}

	return "General"
}

func buildThemeSummary(items []types.SearchResultItem) string {
	if len(items) == 0 {
		return "No items"
	}
	if len(items) == 1 {
		snippet := items[0].Snippet
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		return snippet
	}
	return fmt.Sprintf("%d related results found", len(items))
}

func buildComparisonMap(results []types.SearchResultItem) map[string]SourceInfo {
	comparison := make(map[string]SourceInfo)

	for _, r := range results {
		info := SourceInfo{
			URL:           r.URL,
			Title:         r.Title,
			RecencyScore:  0.5,
			Completeness:  calculateCompleteness(r),
			Reliability:   calculateReliability(r.Source),
		}

		if r.PublishedAt != nil {
			age := time.Since(*r.PublishedAt)
			if age.Hours() < 24 {
				info.RecencyScore = 1.0
			} else if age.Hours() < 168 {
				info.RecencyScore = 0.8
			} else if age.Hours() < 720 {
				info.RecencyScore = 0.6
			} else {
				info.RecencyScore = 0.4
			}
		}

		comparison[r.URL] = info
	}

	return comparison
}

func calculateCompleteness(r types.SearchResultItem) float64 {
	score := 0.5
	if r.Title != "" {
		score += 0.1
	}
	if r.Snippet != "" {
		score += 0.2
	}
	if r.Author != "" {
		score += 0.1
	}
	if r.PublishedAt != nil {
		score += 0.1
	}
	return score
}

func calculateReliability(source string) float64 {
	highReliability := map[string]bool{
		"official": true, "docs": true, "documentation": true,
		"github": true, "stackoverflow": true,
	}
	mediumReliability := map[string]bool{
		"web": true, "news": true, "blog": true,
	}

	if highReliability[strings.ToLower(source)] {
		return 0.9
	}
	if mediumReliability[strings.ToLower(source)] {
		return 0.7
	}
	return 0.5
}

func findConsensusAndDivergences(results []types.SearchResultItem, query string) (string, []string) {
	if len(results) < 2 {
		return "Insufficient sources for consensus analysis.", []string{}
	}

	titleWords := make(map[string]int)
	for _, r := range results {
		words := strings.Fields(strings.ToLower(r.Title))
		for _, w := range words {
			if len(w) > 3 {
				titleWords[w]++
			}
		}
	}

	var commonWords []string
	for word, count := range titleWords {
		if count >= len(results)/2 {
			commonWords = append(commonWords, word)
		}
	}

	if len(commonWords) > 0 {
		return "Sources agree on key aspects: " + strings.Join(commonWords[:min(3, len(commonWords))], ", "), []string{}
	}

	return "No clear consensus - sources cover different aspects.", []string{"Topics vary across sources"}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func formatCount(n int) string {
	switch n {
	case 0:
		return "no"
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	default:
		return fmt.Sprintf("%d", n)
	}
}
