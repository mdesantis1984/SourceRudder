package search

import (
	"context"
	"log"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/normalization"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type ConnectorManager struct {
	connectors map[string]types.SearchConnector
	cache      *cache.Service
}

func NewConnectorManager(cacheSvc *cache.Service) *ConnectorManager {
	return &ConnectorManager{
		connectors: make(map[string]types.SearchConnector),
		cache:      cacheSvc,
	}
}

func (m *ConnectorManager) Register(connector types.SearchConnector) {
	m.connectors[connector.Name()] = connector
	log.Printf("[connector_manager] registered: %s", connector.Name())
}

func (m *ConnectorManager) Search(ctx context.Context, source string, req *types.SearchRequest) (*types.SearchResponse, error) {
	conn, ok := m.connectors[source]
	if !ok {
		return &types.SearchResponse{
			Query:      req.Query,
			Results:    []types.SearchResultItem{},
			Errors:     []string{"unknown source: " + source},
			SourcesUsed: []string{source},
		}, nil
	}
	return conn.Search(ctx, req)
}

func (m *ConnectorManager) SearchAll(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	var allResults []types.SearchResultItem
	sourcesUsed := make(map[string]bool)
	var errors []string

	for name, conn := range m.connectors {
		result, err := conn.Search(ctx, req)
		if err != nil {
			errors = append(errors, name+": "+err.Error())
			continue
		}
		allResults = append(allResults, result.Results...)
		for _, s := range result.SourcesUsed {
			sourcesUsed[s] = true
		}
		if result.Errors != nil {
			errors = append(errors, result.Errors...)
		}
	}

	allResults = deduplicateResults(allResults)

	sources := make([]string, 0, len(sourcesUsed))
	for s := range sourcesUsed {
		sources = append(sources, s)
	}

	return &types.SearchResponse{
		Query:       req.Query,
		Results:     allResults,
		SourcesUsed: sources,
		Errors:      errors,
	}, nil
}

func deduplicateResults(results []types.SearchResultItem) []types.SearchResultItem {
	seen := make(map[string]bool)
	deduped := make([]types.SearchResultItem, 0, len(results))

	for _, r := range results {
		canonical := normalization.ExtractCanonicalURL(r.URL)
		if seen[canonical] {
			continue
		}
		seen[canonical] = true
		r.CanonicalURL = canonical
		deduped = append(deduped, r)
	}

	return deduped
}

func (m *ConnectorManager) GetConnector(name string) (types.SearchConnector, bool) {
	conn, ok := m.connectors[name]
	return conn, ok
}

// Cache returns the in-process cache the ConnectorManager shares
// with the rest of the server. It exists so stateful handlers (e.g.
// the MCP get_cached / invalidate_cache tools restored by the
// restore-runtime-contract change) can reach the same cache instance
// the connectors use without going through the public connector
// surface. The returned pointer is non-nil by construction.
func (m *ConnectorManager) Cache() *cache.Service {
	return m.cache
}
