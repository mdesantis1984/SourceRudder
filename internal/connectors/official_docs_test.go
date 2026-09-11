package connectors

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type officialDocsBackendStub struct {
	responses []*types.SearchResponse
	errors    []error
	requests  []*types.SearchRequest
}

func (s *officialDocsBackendStub) Name() string { return "web" }

func (s *officialDocsBackendStub) Search(_ context.Context, req *types.SearchRequest) (*types.SearchResponse, error) {
	copy := *req
	s.requests = append(s.requests, &copy)
	index := len(s.requests) - 1
	if index < len(s.errors) && s.errors[index] != nil {
		return nil, s.errors[index]
	}
	return s.responses[index], nil
}

func TestOfficialDocsConnectorValidatesRegistryProvenance(t *testing.T) {
	backend := &officialDocsBackendStub{responses: []*types.SearchResponse{{
		Results: []types.SearchResultItem{
			{URL: "https://go.dev/doc/first", Source: "searxng"},
			{URL: "https://docs.go.dev/ref", Source: "searxng"},
			{URL: "https://go.dev.evil.test/doc", Source: "searxng"},
			{URL: "https://example.test/go", Source: "searxng"},
			{URL: "ftp://go.dev/doc", Source: "searxng"},
			{URL: "https://", Source: "searxng"},
		},
	}}}

	resp, err := NewOfficialDocsConnector(backend).Search(context.Background(), &types.SearchRequest{Query: "golang documentation", MaxResults: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if resp.Strategy != officialDocsStrategy || len(resp.Results) != 2 {
		t.Fatalf("expected two official results with %q, got %#v", officialDocsStrategy, resp)
	}
	if got := []string{resp.Results[0].URL, resp.Results[1].URL}; strings.Join(got, ",") != "https://go.dev/doc/first,https://docs.go.dev/ref" {
		t.Errorf("expected preserved official result order, got %v", got)
	}
	if backend.requests[0].MaxResults != officialDocsFetchLimit || !strings.Contains(backend.requests[0].Query, "site:go.dev") {
		t.Errorf("expected bounded Go-scoped backend request, got %#v", backend.requests[0])
	}
	for _, result := range resp.Results {
		if result.Source != "official_docs" || !hasTag(result.Tags, "official-documentation") || !hasTag(result.Tags, "library:go") {
			t.Errorf("expected authoritative provenance, got %#v", result)
		}
	}
}

func TestOfficialDocsConnectorResolutionAndFallback(t *testing.T) {
	tests := []struct {
		name         string
		req          *types.SearchRequest
		responses    []*types.SearchResponse
		errors       []error
		wantStrategy string
		wantCalls    int
		wantScoped   bool
		wantWarning  string
	}{
		{
			name:         "explicit library takes precedence over query inference",
			req:          &types.SearchRequest{Query: "searxng go docs", Filters: map[string]string{"library": "golang"}},
			responses:    []*types.SearchResponse{{Results: []types.SearchResultItem{{URL: "https://go.dev/doc/"}}}},
			wantStrategy: officialDocsStrategy, wantCalls: 1, wantScoped: true,
		},
		{
			name:         "whole token aliases do not match substrings",
			req:          &types.SearchRequest{Query: "gopher documentation"},
			responses:    []*types.SearchResponse{{Results: []types.SearchResultItem{{URL: "https://example.test/fallback"}}}},
			wantStrategy: officialDocsFallbackStrategy, wantCalls: 1,
		},
		{
			name:         "ambiguous libraries fall back honestly",
			req:          &types.SearchRequest{Query: "go searxng documentation"},
			responses:    []*types.SearchResponse{{Results: []types.SearchResultItem{{URL: "https://example.test/fallback"}}}},
			wantStrategy: officialDocsFallbackStrategy, wantCalls: 1,
		},
		{
			name:         "unknown explicit library falls back honestly",
			req:          &types.SearchRequest{Query: "go documentation", Filters: map[string]string{"library": "unknown"}},
			responses:    []*types.SearchResponse{{Results: []types.SearchResultItem{{URL: "https://example.test/fallback"}}}},
			wantStrategy: officialDocsFallbackStrategy, wantCalls: 1,
		},
		{
			name:         "unverified version is never claimed",
			req:          &types.SearchRequest{Query: "go documentation", Filters: map[string]string{"version": "1.99"}},
			responses:    []*types.SearchResponse{{Results: []types.SearchResultItem{{URL: "https://go.dev/doc/"}}}},
			wantStrategy: officialDocsStrategy, wantCalls: 1, wantScoped: true, wantWarning: "not verified",
		},
		{
			name: "zero validated official results return useful fallback",
			req:  &types.SearchRequest{Query: "go documentation"},
			responses: []*types.SearchResponse{
				{Results: []types.SearchResultItem{{URL: "https://example.test/unapproved"}}},
				{Results: []types.SearchResultItem{{URL: "https://example.test/fallback"}}},
			},
			wantStrategy: officialDocsFallbackStrategy, wantCalls: 2, wantScoped: true, wantWarning: "no validated",
		},
		{
			name:         "backend error returns useful fallback with warning",
			req:          &types.SearchRequest{Query: "go documentation"},
			responses:    []*types.SearchResponse{nil, {Results: []types.SearchResultItem{{URL: "https://example.test/fallback"}}}},
			errors:       []error{errors.New("upstream unavailable")},
			wantStrategy: officialDocsFallbackStrategy, wantCalls: 2, wantScoped: true, wantWarning: "upstream unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &officialDocsBackendStub{responses: tt.responses, errors: tt.errors}
			resp, err := NewOfficialDocsConnector(backend).Search(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if resp.Strategy != tt.wantStrategy || len(backend.requests) != tt.wantCalls {
				t.Fatalf("strategy=%q calls=%d, want strategy=%q calls=%d", resp.Strategy, len(backend.requests), tt.wantStrategy, tt.wantCalls)
			}
			if tt.wantScoped != strings.Contains(backend.requests[0].Query, "site:") {
				t.Errorf("first query %q scoped=%v, want %v", backend.requests[0].Query, strings.Contains(backend.requests[0].Query, "site:"), tt.wantScoped)
			}
			if tt.wantWarning != "" && !warningsContain(resp.Warnings, tt.wantWarning) {
				t.Errorf("expected warning containing %q, got %v", tt.wantWarning, resp.Warnings)
			}
			if tt.wantStrategy == officialDocsFallbackStrategy && len(resp.Results) == 0 {
				t.Error("fallback must preserve useful backend results")
			}
		})
	}
}

func hasTag(tags []string, want string) bool {
	for _, tag := range tags {
		if tag == want {
			return true
		}
	}
	return false
}

func warningsContain(warnings []string, want string) bool {
	for _, warning := range warnings {
		if strings.Contains(warning, want) {
			return true
		}
	}
	return false
}
