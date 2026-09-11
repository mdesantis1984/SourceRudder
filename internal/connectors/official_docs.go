package connectors

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

const (
	officialDocsStrategy         = "official_doc_registry_search"
	officialDocsFallbackStrategy = "official_doc_web_fallback"
	officialDocsFetchLimit       = 50
)

type officialLibrary struct {
	id      string
	aliases []string
	hosts   []string
}

var officialLibraries = []officialLibrary{
	{id: "go", aliases: []string{"go", "golang"}, hosts: []string{"go.dev"}},
	{id: "searxng", aliases: []string{"searxng", "searx"}, hosts: []string{"docs.searxng.org"}},
}

// OfficialDocsConnector uses the existing web connector only as a SearXNG
// execution substrate. The registry and URL provenance validation stay local.
type OfficialDocsConnector struct {
	backend types.SearchConnector
}

func NewOfficialDocsConnector(backend types.SearchConnector) *OfficialDocsConnector {
	return &OfficialDocsConnector{backend: backend}
}

func (c *OfficialDocsConnector) Name() string { return "official_docs" }

func (c *OfficialDocsConnector) Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	library, ok := resolveOfficialLibrary(req)
	if !ok {
		return c.fallback(ctx, req, "no unambiguous library in the authoritative documentation registry")
	}

	scopedReq := *req
	scopedReq.Query = scopedOfficialQuery(req.Query, library.hosts)
	if scopedReq.MaxResults < officialDocsFetchLimit {
		scopedReq.MaxResults = officialDocsFetchLimit
	}

	resp, err := c.backend.Search(ctx, &scopedReq)
	if err != nil {
		return c.fallback(ctx, req, "official registry search failed: "+err.Error())
	}

	results := filterOfficialResults(resp.Results, library.hosts, req.MaxResults, library.id)
	if len(results) == 0 {
		return c.fallback(ctx, req, "no validated authoritative results survived registry provenance checks")
	}

	resp.Query = req.Query
	resp.Results = results
	resp.Strategy = officialDocsStrategy
	resp.SourcesUsed = []string{c.Name()}
	if version := requestFilter(req, "version"); version != "" {
		resp.Warnings = append(resp.Warnings, "version filter "+fmt.Sprintf("%q", version)+" is not verified by the authoritative registry")
	}
	return resp, nil
}

func (c *OfficialDocsConnector) fallback(ctx context.Context, req *types.SearchRequest, reason string) (*types.SearchResponse, error) {
	resp, err := c.backend.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	resp.Strategy = officialDocsFallbackStrategy
	resp.Warnings = append(resp.Warnings, "strategy: official_doc_web_fallback — "+reason)
	return resp, nil
}

func resolveOfficialLibrary(req *types.SearchRequest) (officialLibrary, bool) {
	if requested := requestFilter(req, "library"); requested != "" {
		return resolveOfficialAlias(strings.ToLower(strings.TrimSpace(requested)))
	}

	matches := make(map[string]officialLibrary)
	for _, token := range wholeTokens(req.Query) {
		if library, ok := resolveOfficialAlias(token); ok {
			matches[library.id] = library
		}
	}
	if len(matches) != 1 {
		return officialLibrary{}, false
	}
	for _, library := range matches {
		return library, true
	}
	return officialLibrary{}, false
}

func resolveOfficialAlias(alias string) (officialLibrary, bool) {
	for _, library := range officialLibraries {
		for _, candidate := range library.aliases {
			if alias == candidate {
				return library, true
			}
		}
	}
	return officialLibrary{}, false
}

func requestFilter(req *types.SearchRequest, key string) string {
	if req.Filters == nil {
		return ""
	}
	return strings.TrimSpace(req.Filters[key])
}

func wholeTokens(query string) []string {
	return strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func scopedOfficialQuery(query string, hosts []string) string {
	clauses := make([]string, 0, len(hosts))
	for _, host := range hosts {
		clauses = append(clauses, "site:"+host)
	}
	return strings.Join(clauses, " OR ") + " " + query
}

func filterOfficialResults(results []types.SearchResultItem, hosts []string, maxResults int, libraryID string) []types.SearchResultItem {
	limit := getMaxResults(maxResults, 10)
	filtered := make([]types.SearchResultItem, 0, limit)
	for _, result := range results {
		if len(filtered) == limit || !isApprovedOfficialURL(result.URL, hosts) {
			continue
		}
		result.Source = "official_docs"
		result.Tags = append(result.Tags, "official-documentation", "library:"+libraryID)
		filtered = append(filtered, result)
	}
	return filtered
}

func isApprovedOfficialURL(rawURL string, approvedHosts []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, approved := range approvedHosts {
		approved = strings.ToLower(approved)
		if host == approved || strings.HasSuffix(host, "."+approved) {
			return true
		}
	}
	return false
}
