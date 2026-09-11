package search

import (
	"strings"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type Planner struct{}

func NewPlanner() *Planner {
	return &Planner{}
}

type SearchPlan struct {
	Query      string
	Connectors []string
	MaxResults int
	TimeRange  string
	Intent     string
	Priority   int
	Strategy   string
}

func (p *Planner) Plan(query string, req *types.SearchRequest) *SearchPlan {
	intent := p.ClassifyIntent(query)
	sources := p.SelectSources(intent, req)
	maxResults := p.resolveMaxResults(req)
	timeRange := p.resolveTimeRange(req, intent)
	priority := p.resolvePriority(intent)

	return &SearchPlan{
		Query:      query,
		Connectors: sources,
		MaxResults: maxResults,
		TimeRange:  timeRange,
		Intent:     intent,
		Priority:   priority,
		Strategy:   p.resolveStrategy(intent, sources),
	}
}

func (p *Planner) ClassifyIntent(query string) string {
	lower := strings.ToLower(query)

	intentScores := map[string]int{
		"technical": 0,
		"academic":  0,
		"code":      0,
		"news":      0,
	}

	technicalKeywords := []string{"api", "error", "bug", "code", "function", "class", "debug", "sdk", "library", "framework", "module", "package", "dependency", "install", "config", "docker", "kubernetes", "deploy", "ci/cd", "pipeline", "database", "query", "sql", "nosql", "cache", "queue", "message"}
	academicKeywords := []string{"paper", "research", "study", "academic", "journal", "conference", "publication", "citation", "doi", "arxiv", "preprint", "thesis", "dissertation", "abstract", "methodology", "results", "conclusion"}
	codeKeywords := []string{"github", "repo", "stackoverflow", "npm", "pypi", "nuget", "crates.io", "packagist", "gem", "pip", "npmjs", "stackoverflow.com", "repository", "commit", "branch", "pull request", "issue", "stack trace", "exception", "runtime"}
	newsKeywords := []string{"news", "latest", "today", "breaking", "announcement", "release", "launch", "update", "recent", "trending", "viral", "headline"}
	youtubeKeywords := []string{"youtube", "video", "tutorial", "watch", "channel", "invidious"}
	packageKeywords := []string{"nuget", "npm", "pypi", "pip", "gem", "cargo", "maven", "gradle", "packagist", "crates"}

	for _, kw := range technicalKeywords {
		if strings.Contains(lower, kw) {
			intentScores["technical"]++
		}
	}
	for _, kw := range academicKeywords {
		if strings.Contains(lower, kw) {
			intentScores["academic"]++
		}
	}
	for _, kw := range codeKeywords {
		if strings.Contains(lower, kw) {
			intentScores["code"]++
		}
	}
	for _, kw := range newsKeywords {
		if strings.Contains(lower, kw) {
			intentScores["news"]++
		}
	}
	for _, kw := range youtubeKeywords {
		if strings.Contains(lower, kw) {
			intentScores["video"]++
		}
	}
	for _, kw := range packageKeywords {
		if strings.Contains(lower, kw) {
			intentScores["package"]++
		}
	}

	maxScore := 0
	selectedIntent := "general"
	for intent, score := range intentScores {
		if score > maxScore {
			maxScore = score
			selectedIntent = intent
		}
	}

	return selectedIntent
}

func (p *Planner) SelectSources(intent string, req *types.SearchRequest) []string {
	if len(req.Sources) > 0 {
		return req.Sources
	}

	switch intent {
	case "code":
		return []string{"github", "stackoverflow"}
	case "technical":
		return []string{"web", "stackoverflow"}
	case "academic":
		return []string{"academic"}
	case "news":
		return []string{"web"}
	case "video":
		return []string{"youtube"}
	case "package":
		return []string{"nuget", "npm", "pypi"}
	default:
		return []string{"web"}
	}
}

func (p *Planner) resolveMaxResults(req *types.SearchRequest) int {
	if req.MaxResults > 0 {
		return req.MaxResults
	}
	return 10
}

func (p *Planner) resolveTimeRange(req *types.SearchRequest, intent string) string {
	if req.TimeRange != "" {
		return req.TimeRange
	}
	if intent == "news" {
		return "week"
	}
	return ""
}

func (p *Planner) resolvePriority(intent string) int {
	switch intent {
	case "code":
		return 1
	case "technical":
		return 2
	case "academic":
		return 3
	case "news":
		return 2
	default:
		return 4
	}
}

func (p *Planner) resolveStrategy(intent string, sources []string) string {
	if len(sources) > 2 {
		return "parallel"
	}
	return "single"
}