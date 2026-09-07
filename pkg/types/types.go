package types

import (
	"context"
	"time"
)

type SearchRequest struct {
	Query        string            `json:"query"`
	Sources      []string          `json:"sources,omitempty"`
	MaxResults   int               `json:"maxResults,omitempty"`
	Language     string            `json:"language,omitempty"`
	SafeSearch   bool              `json:"safeSearch,omitempty"`
	TimeRange    string            `json:"timeRange,omitempty"`
	Format       string            `json:"format,omitempty"`
	CachePolicy  string            `json:"cachePolicy,omitempty"`
	DeepResearch bool              `json:"deepResearch,omitempty"`
	Filters      map[string]string `json:"filters,omitempty"`
}

type SearchResultItem struct {
	Title        string     `json:"title"`
	URL          string     `json:"url"`
	Snippet      string     `json:"snippet,omitempty"`
	Source       string     `json:"source"`
	Type         string     `json:"type,omitempty"`
	Score        float64    `json:"score,omitempty"`
	PublishedAt  *time.Time `json:"publishedAt,omitempty"`
	Author       string     `json:"author,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
	CitationID   string     `json:"citationId,omitempty"`
	CanonicalURL string     `json:"canonicalUrl,omitempty"`
}

// SearchResponse is the stable wire contract returned by every MCP search
// tool. The Results field is always serialized as a JSON array, never null
// or omitted, even when empty, so downstream AI agents can iterate over it
// without nil checks. Strategy, when non-empty, names the concrete backend
// that produced the response (e.g. "reddit", "searxng", "official_doc_web_fallback",
// "local_index_lexical", "local_index_unavailable"); it lets the AI distinguish a genuine empty
// result from a tool that is not actually wired.
type SearchResponse struct {
	Query       string             `json:"query"`
	Results     []SearchResultItem `json:"results"`
	Summary     string             `json:"summary,omitempty"`
	KeyFindings []string           `json:"keyFindings,omitempty"`
	SourcesUsed []string           `json:"sourcesUsed,omitempty"`
	Strategy    string             `json:"strategy,omitempty"`
	Confidence  float64            `json:"confidence,omitempty"`
	Cached      bool               `json:"cached,omitempty"`
	Partial     bool               `json:"partial,omitempty"`
	Warnings    []string           `json:"warnings,omitempty"`
	Errors      []string           `json:"errors,omitempty"`
}

type FetchResponse struct {
	URL      string            `json:"url"`
	Title    string            `json:"title,omitempty"`
	Content  string            `json:"content,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`

	// Outcome names the final classification of the fetch. Allowed
	// values (Phase 6 wire contract): "success", "blocked-target",
	// "blocked-redirect", "too-many-redirects", "timeout",
	// "transport-error", "http-error", "non-transient-failure",
	// "transient-failure-retried-exhausted". MCP clients should switch
	// on this value instead of inspecting the Go error string.
	Outcome string `json:"outcome,omitempty"`

	// Status is the upstream HTTP status code or zero when no response
	// was received (timeout, transport error, blocked).
	Status int `json:"status,omitempty"`

	// RedirectChain records every Location the fetcher followed,
	// excluding the initial URL. Empty when the request did not
	// redirect or never reached a response.
	RedirectChain []string `json:"redirectChain,omitempty"`

	// Attempts counts how many times the fetcher tried to reach the
	// target, including transient retries. 1 means no retry occurred.
	Attempts int `json:"attempts,omitempty"`
}

type SearchConnector interface {
	Name() string
	Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

type CacheEntry struct {
	CacheKey  string    `json:"cacheKey"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	Payload   []byte    `json:"payload,omitempty"`
	SourceSet []string  `json:"sourceSet,omitempty"`
}
