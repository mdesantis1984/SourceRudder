package search

import (
	"context"
	"fmt"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type Registry struct {
	connectors map[string]Connector
}

type Connector interface {
	Search(ctx context.Context, req *types.SearchRequest) (*types.SearchResponse, error)
	Name() string
}

func NewRegistry() *Registry {
	return &Registry{
		connectors: make(map[string]Connector),
	}
}

func (r *Registry) Register(conn Connector) {
	r.connectors[conn.Name()] = conn
}

func (r *Registry) Get(name string) (Connector, error) {
	conn, ok := r.connectors[name]
	if !ok {
		return nil, fmt.Errorf("connector not found: %s", name)
	}
	return conn, nil
}

func (r *Registry) List() []string {
	names := make([]string, 0, len(r.connectors))
	for name := range r.connectors {
		names = append(names, name)
	}
	return names
}

func (r *Registry) Search(ctx context.Context, name string, req *types.SearchRequest) (*types.SearchResponse, error) {
	conn, err := r.Get(name)
	if err != nil {
		return nil, err
	}
	return conn.Search(ctx, req)
}
