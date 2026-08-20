package search

import (
	"context"
	"log"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/normalization"
	"github.com/thiscloud/ia-buscar/pkg/types"
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
