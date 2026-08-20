package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/thiscloud/ia-buscar/internal/connectors"
	"github.com/thiscloud/ia-buscar/internal/search"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

func (s *Server) registerTools() {
	for i := range s.toolsRegistry {
		t := &s.toolsRegistry[i]
		switch t.Name {
		case "search_web":
			t.Handler = s.makeSearchHandler("web")
		case "search_github":
			t.Handler = s.makeSearchHandler("github")
		case "search_github_pr":
			t.Handler = s.makeGitHubPRHandler()
		case "search_github_issue":
			t.Handler = s.makeGitHubIssueHandler()
		case "search_stackoverflow":
			t.Handler = s.makeSearchHandler("stackoverflow")
		case "search_npm":
			t.Handler = s.makeSearchHandler("npm")
		case "search_nuget":
			t.Handler = s.makeSearchHandler("nuget")
		case "search_pypi":
			t.Handler = s.makeSearchHandler("pypi")
		case "search_docker_hub":
			t.Handler = s.makeSearchHandler("dockerhub")
		case "search_doc_oficial":
			t.Handler = s.makeDocOficialHandler()
		case "search_local_index":
			t.Handler = s.makeLocalIndexHandler()
		case "search_academic":
			t.Handler = s.makeSearchHandler("academic")
		case "search_reddit":
			t.Handler = s.makeSearchHandler("reddit")
		case "search_youtube":
			t.Handler = s.makeSearchHandler("youtube")
		case "search_images":
			t.Handler = s.makeSearchHandler("images")
		case "search_news":
			t.Handler = s.makeSearchHandler("news")
		case "fetch_url":
			t.Handler = s.makeFetchHandler("fetch")
		case "fetch_and_extract":
			t.Handler = s.makeFetchHandler("fetch_and_extract")
		case "extract_structured":
			t.Handler = s.makeFetchHandler("extract_structured")
		case "validate_url":
			t.Handler = s.makeValidateHandler("validate_url")
		case "check_link_status":
			t.Handler = s.makeValidateHandler("check_link_status")
		case "summarize_results":
			t.Handler = s.makeSynthesizeHandler("summarize")
		case "deep_research":
			t.Handler = s.makeSynthesizeHandler("deep_research")
		case "compare_sources":
			t.Handler = s.makeSynthesizeHandler("compare_sources")
		case "get_current_date":
			t.Handler = s.getCurrentDateHandler
		}
	}
}

func (s *Server) makeSearchHandler(source string) func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req types.SearchRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		if s.connectorManager == nil {
			return normalizeSearchResponse(&types.SearchResponse{
				Query:       req.Query,
				Results:     []types.SearchResultItem{},
				Errors:      []string{"connector manager not initialized"},
			}), nil
		}

		var plan *search.SearchPlan
		if s.planner != nil {
			plan = s.planner.Plan(req.Query, &req)
		}

		resp, err := s.connectorManager.Search(ctx, source, &req)
		if err != nil {
			return nil, err
		}

		if plan != nil {
			if len(plan.Connectors) > 0 {
				resp.SourcesUsed = []string{source}
			}
			if plan.Intent != "" {
				resp.Warnings = append(resp.Warnings, "intent: "+plan.Intent)
			}
		}

		return normalizeSearchResponse(resp), nil
	}
}

// makeDocOficialHandler is the truthful fallback for search_doc_oficial.
// No specialized official-documentation provider is wired into IA_Buscar
// today, so the tool explicitly says so: it falls back to the configured
// web connector (SearxNG) and stamps Strategy = "official_doc_web_fallback"
// plus a warning naming the source. AI agents reading the response can tell
// that no real official-doc index was queried.
func (s *Server) makeDocOficialHandler() func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req types.SearchRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		if s.connectorManager == nil {
			return normalizeSearchResponse(&types.SearchResponse{
				Query:       req.Query,
				Results:     []types.SearchResultItem{},
				Strategy:    "official_doc_web_fallback",
				SourcesUsed: []string{},
				Warnings: []string{
					"strategy: official_doc_web_fallback — no specialized official-documentation provider is wired; falling back to general web search",
				},
				Errors: []string{"connector manager not initialized"},
			}), nil
		}

		resp, err := s.connectorManager.Search(ctx, "web", &req)
		if err != nil {
			return nil, err
		}

		resp.Strategy = "official_doc_web_fallback"
		if resp.SourcesUsed == nil {
			resp.SourcesUsed = []string{}
		}
		resp.Warnings = append(resp.Warnings,
			"strategy: official_doc_web_fallback — results came from general web search (SearxNG), not from a curated official-documentation index")

		return normalizeSearchResponse(resp), nil
	}
}

// makeLocalIndexHandler returns an explicit unavailable signal instead of
// silently routing to a generic web search. Until a real local-index
// provider is wired (e.g. a workspace embedder or a downloaded corpus),
// the tool returns a stable empty result with Strategy =
// "local_index_unavailable" and a warning the AI can act on.
func (s *Server) makeLocalIndexHandler() func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req types.SearchRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}

		return normalizeSearchResponse(&types.SearchResponse{
			Query:       req.Query,
			Results:     []types.SearchResultItem{},
			Strategy:    "local_index_unavailable",
			SourcesUsed: []string{},
			Partial:     false,
			Warnings: []string{
				"local_index_unavailable: no local-index provider is configured for IA_Buscar; this tool does not fall back to web search",
			},
			Errors: []string{
				"local_index_unavailable",
			},
		}), nil
	}
}

// normalizeSearchResponse guarantees the stable wire contract: every
// response handed back to an MCP client serializes Results as a JSON
// array (never null, never omitted), even when the underlying connector
// or cache-decoded payload left it nil. It is intentionally idempotent.
func normalizeSearchResponse(resp *types.SearchResponse) *types.SearchResponse {
	if resp == nil {
		return &types.SearchResponse{Results: []types.SearchResultItem{}}
	}
	if resp.Results == nil {
		resp.Results = []types.SearchResultItem{}
	}
	if resp.SourcesUsed == nil {
		resp.SourcesUsed = []string{}
	}
	if resp.Warnings == nil {
		resp.Warnings = []string{}
	}
	if resp.Errors == nil {
		resp.Errors = []string{}
	}
	if resp.KeyFindings == nil {
		resp.KeyFindings = []string{}
	}
	return resp
}

func (s *Server) makeGitHubPRHandler() func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req types.SearchRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		conn, ok := s.connectorManager.GetConnector("github")
		if !ok {
			return normalizeSearchResponse(&types.SearchResponse{
				Query:   req.Query,
				Results: []types.SearchResultItem{},
				Errors:  []string{"github connector not available"},
			}), nil
		}
		ghConn, ok := conn.(*connectors.GitHubConnector)
		if !ok {
			return normalizeSearchResponse(&types.SearchResponse{
				Query:   req.Query,
				Results: []types.SearchResultItem{},
				Errors:  []string{"invalid github connector type"},
			}), nil
		}
		resp, err := ghConn.SearchPR(ctx, &req)
		if err != nil {
			return nil, err
		}
		return normalizeSearchResponse(resp), nil
	}
}

func (s *Server) makeGitHubIssueHandler() func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req types.SearchRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		conn, ok := s.connectorManager.GetConnector("github")
		if !ok {
			return normalizeSearchResponse(&types.SearchResponse{
				Query:   req.Query,
				Results: []types.SearchResultItem{},
				Errors:  []string{"github connector not available"},
			}), nil
		}
		ghConn, ok := conn.(*connectors.GitHubConnector)
		if !ok {
			return normalizeSearchResponse(&types.SearchResponse{
				Query:   req.Query,
				Results: []types.SearchResultItem{},
				Errors:  []string{"invalid github connector type"},
			}), nil
		}
		resp, err := ghConn.SearchIssue(ctx, &req)
		if err != nil {
			return nil, err
		}
		return normalizeSearchResponse(resp), nil
	}
}

func (s *Server) makeFetchHandler(op string) func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req struct {
			URL  string `json:"url"`
			Mode string `json:"mode"`
		}
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		if req.Mode == "" {
			req.Mode = "auto"
		}
		switch op {
		case "fetch":
			return s.fetcherService.Fetch(ctx, req.URL)
		case "fetch_and_extract":
			return s.fetcherService.FetchAndExtract(ctx, req.URL, req.Mode)
		case "extract_structured":
			return s.fetcherService.ExtractStructured(ctx, req.URL)
		}
		return nil, fmt.Errorf("unknown fetch operation: %s", op)
	}
}

func (s *Server) makeValidateHandler(op string) func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req struct {
			URL  string   `json:"url"`
			URLs []string `json:"urls"`
		}
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		switch op {
		case "validate_url":
			valid, err := s.fetcherService.ValidateURL(ctx, req.URL)
			var errMsg string
			if err != nil {
				errMsg = err.Error()
			}
			return map[string]interface{}{
				"url":   req.URL,
				"valid": valid,
				"error": errMsg,
			}, nil
		case "check_link_status":
			return s.fetcherService.CheckLinkStatus(ctx, req.URLs)
		}
		return nil, fmt.Errorf("unknown validate operation: %s", op)
	}
}

func (s *Server) stubSearchHandler(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var req types.SearchRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	return &types.SearchResponse{
		Query:       req.Query,
		Results:     []types.SearchResultItem{},
		Summary:     "[STUB] Search not yet implemented",
		SourcesUsed:  []string{},
		Confidence:   0.0,
		Cached:       false,
		Warnings:     []string{"Stub implementation"},
	}, nil
}

func (s *Server) stubFetchHandler(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var req struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	return &types.FetchResponse{
		URL:      req.URL,
		Title:    "[STUB] Title",
		Content:  "[STUB] Content",
		Metadata: map[string]string{},
		Warnings: []string{"Stub implementation"},
	}, nil
}

func (s *Server) stubValidateHandler(ctx context.Context, args json.RawMessage) (interface{}, error) {
	var req struct {
		URL  string   `json:"url"`
		URLs []string `json:"urls"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if req.URL != "" {
		return map[string]interface{}{
			"url":     req.URL,
			"valid":   true,
			"warnings": []string{"Stub implementation"},
		}, nil
	}
	results := make([]map[string]interface{}, 0)
	for _, u := range req.URLs {
		results = append(results, map[string]interface{}{"url": u, "valid": true, "status": 200})
	}
	return map[string]interface{}{"results": results, "warnings": []string{"Stub implementation"}}, nil
}

func (s *Server) makeSynthesizeHandler(op string) func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req struct {
			Query   string                  `json:"query"`
			Results []types.SearchResultItem `json:"results"`
			Goal    string                  `json:"goal"`
			Style   string                  `json:"style"`
		}
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}

		if s.synthesisService == nil {
			return map[string]interface{}{
				"summary":     "[STUB] Synthesis service not initialized",
				"keyFindings": []string{},
				"citations":   []string{},
				"query":       req.Query,
				"warnings":    []string{"Synthesis service not initialized"},
			}, nil
		}

		switch op {
		case "summarize":
			result, err := s.synthesisService.Summarize(ctx, req.Query, req.Results)
			if err != nil {
				return nil, err
			}
			return result, nil
		case "deep_research":
			result, err := s.synthesisService.DeepResearch(ctx, req.Query, req.Results)
			if err != nil {
				return nil, err
			}
			return result, nil
		case "compare_sources":
			result, err := s.synthesisService.CompareSources(ctx, req.Results, req.Query)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
		return nil, fmt.Errorf("unknown synthesis operation: %s", op)
	}
}

func (s *Server) getCurrentDateHandler(ctx context.Context, args json.RawMessage) (interface{}, error) {
	now := time.Now().UTC()
	return map[string]interface{}{
		"date":      now.Format("2006-01-02"),
		"time":      now.Format("15:04:05"),
		"timezone":  "UTC",
		"timestamp": now.Unix(),
	}, nil
}

func (s *Server) ListTools() []map[string]interface{} {
	tools := make([]map[string]interface{}, 0, len(s.toolsRegistry))
	for _, t := range s.toolsRegistry {
		tools = append(tools, map[string]interface{}{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return tools
}
