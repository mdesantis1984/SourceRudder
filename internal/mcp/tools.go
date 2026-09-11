package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/search"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type githubPRSearcher interface {
	SearchPR(context.Context, *types.SearchRequest) (*types.SearchResponse, error)
}

type githubIssueSearcher interface {
	SearchIssue(context.Context, *types.SearchRequest) (*types.SearchResponse, error)
}

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
		case "get_cached":
			t.Handler = s.makeGetCachedHandler()
		case "invalidate_cache":
			t.Handler = s.makeInvalidateCacheHandler()
		case "get_search_history":
			t.Handler = s.makeGetSearchHistoryHandler()
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
				Query:   req.Query,
				Results: []types.SearchResultItem{},
				Errors:  []string{"connector manager not initialized"},
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

		// Record the completed production search into the in-process
		// HistoryService so get_search_history returns organically
		// recorded invocations instead of an empty list. Recording
		// happens after the connector returns successfully — failed
		// calls are intentionally not recorded so the history reflects
		// what the operator actually saw, not what we tried.
		s.recordSearch(ctx, source, resp)

		return normalizeSearchResponse(resp), nil
	}
}

// makeDocOficialHandler uses the registry-backed official-doc connector when
// a library resolves unambiguously, while preserving the typed web fallback.
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

		resp, err := s.connectorManager.Search(ctx, "official_docs", &req)
		if err == nil && len(resp.Errors) == 1 && strings.HasPrefix(resp.Errors[0], "unknown source:") {
			resp, err = s.connectorManager.Search(ctx, "web", &req)
		}
		if err != nil {
			return nil, err
		}

		if resp.Strategy == "" {
			resp.Strategy = "official_doc_web_fallback"
			resp.Warnings = append(resp.Warnings,
				"strategy: official_doc_web_fallback — results came from general web search (SearxNG), not from the authoritative documentation registry")
		}

		// Record the completed production search with the connector
		source := "web"
		if resp.Strategy == "official_doc_registry_search" {
			source = "official_docs"
		}
		s.recordSearch(ctx, source, resp)

		return normalizeSearchResponse(resp), nil
	}
}

// makeLocalIndexHandler uses the explicitly configured read-only corpus when
// present. Without one it returns an honest unavailable signal and never
// silently routes to generic web search.
func (s *Server) makeLocalIndexHandler() func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req types.SearchRequest
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		if s.connectorManager != nil {
			if _, ok := s.connectorManager.GetConnector("local_index"); ok {
				resp, err := s.connectorManager.Search(ctx, "local_index", &req)
				if err != nil {
					return nil, err
				}
				s.recordSearch(ctx, "local_index", resp)
				return normalizeSearchResponse(resp), nil
			}
		}

		return normalizeSearchResponse(&types.SearchResponse{
			Query:       req.Query,
			Results:     []types.SearchResultItem{},
			Strategy:    "local_index_unavailable",
			SourcesUsed: []string{},
			Partial:     false,
			Warnings: []string{
				"local_index_unavailable: no local-index provider is configured for SourceRudder; this tool does not fall back to web search",
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
		ghConn, ok := conn.(githubPRSearcher)
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
		// Record the completed production search so get_search_history
		// returns PR queries alongside regular search_* calls. Source
		// is the connector name (github) for consistency with the
		// search_github / search_github_issue handlers and the simple
		// search_* handlers (which already record the connector name).
		s.recordSearch(ctx, "github", resp)
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
		ghConn, ok := conn.(githubIssueSearcher)
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
		// Record the completed production search so get_search_history
		// returns issue queries alongside regular search_* calls. Source
		// is the connector name (github) for consistency with the
		// search_github / search_github_pr handlers and the simple
		// search_* handlers.
		s.recordSearch(ctx, "github", resp)
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
		SourcesUsed: []string{},
		Confidence:  0.0,
		Cached:      false,
		Warnings:    []string{"Stub implementation"},
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
			"url":      req.URL,
			"valid":    true,
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
			Query   string                   `json:"query"`
			Results []types.SearchResultItem `json:"results"`
			Goal    string                   `json:"goal"`
			Style   string                   `json:"style"`
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

// makeGetCachedHandler returns the get_cached tool handler. The
// handler is intentionally narrow: it accepts a single `key`
// argument and returns JSON
// `{"cache_hit": <bool>, "entry": <CacheEntry|null> }` decoded
// from the same cache the connectors share. A miss (no entry, or
// only an expired entry) returns `cache_hit: false, entry: null`
// without surfacing an error — the contract is "tell me whether
// you have it" not "fail when you don't". Empty / missing key is
// rejected as an invalid argument so the MCP client never
// accidentally queries the whole cache.
func (s *Server) makeGetCachedHandler() func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		if req.Key == "" {
			return nil, fmt.Errorf("key is required")
		}
		if s.connectorManager == nil {
			return map[string]interface{}{
				"cache_hit": false,
				"entry":     nil,
			}, nil
		}
		cacheSvc := s.connectorManager.Cache()
		entry, ok, err := cacheSvc.Get(ctx, req.Key)
		if err != nil {
			return nil, fmt.Errorf("cache get: %w", err)
		}
		if !ok || entry == nil {
			return map[string]interface{}{
				"cache_hit": false,
				"entry":     nil,
			}, nil
		}
		return map[string]interface{}{
			"cache_hit": true,
			"entry": map[string]interface{}{
				"cacheKey":  entry.CacheKey,
				"createdAt": entry.CreatedAt,
				"expiresAt": entry.ExpiresAt,
				"payload":   string(entry.Payload),
				"sourceSet": entry.SourceSet,
			},
		}, nil
	}
}

// makeInvalidateCacheHandler returns the invalidate_cache tool
// handler. The handler calls DeleteIfPresent so the wire response
// can distinguish "I removed your entry" from "there was nothing
// to remove" without a follow-up Get. Both outcomes are successful
// tools/call responses; only an empty key surfaces as an error.
func (s *Server) makeInvalidateCacheHandler() func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		if req.Key == "" {
			return nil, fmt.Errorf("key is required")
		}
		invalidated := false
		if s.connectorManager != nil {
			invalidated = s.connectorManager.Cache().DeleteIfPresent(ctx, req.Key)
		}
		return map[string]interface{}{
			"key":         req.Key,
			"invalidated": invalidated,
		}, nil
	}
}

// makeGetSearchHistoryHandler returns the get_search_history tool
// handler. The handler is intentionally narrow: it accepts
// `limit` (required, positive integer) and `query` (optional
// case-sensitive substring) and returns JSON
// `{"history": [Entry], "limit": N, "query": "..."}`. A missing
// `limit` is rejected as an invalid argument; the HistoryService
// itself enforces its own bound so an oversized limit just returns
// everything up to the bound.
func (s *Server) makeGetSearchHistoryHandler() func(ctx context.Context, args json.RawMessage) (interface{}, error) {
	return func(ctx context.Context, args json.RawMessage) (interface{}, error) {
		var req struct {
			Limit int    `json:"limit"`
			Query string `json:"query"`
		}
		if err := json.Unmarshal(args, &req); err != nil {
			return nil, fmt.Errorf("invalid args: %w", err)
		}
		if req.Limit <= 0 {
			return nil, fmt.Errorf("limit is required and must be positive")
		}
		if s.history == nil {
			return map[string]interface{}{
				"history": []interface{}{},
				"limit":   req.Limit,
				"query":   req.Query,
			}, nil
		}
		entries := s.history.List(ctx, req.Limit, req.Query)
		history := make([]map[string]interface{}, len(entries))
		for i, e := range entries {
			history[i] = map[string]interface{}{
				"query":     e.Query,
				"source":    e.Source,
				"timestamp": e.Timestamp,
			}
		}
		return map[string]interface{}{
			"history": history,
			"limit":   req.Limit,
			"query":   req.Query,
		}, nil
	}
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

// recordSearch appends one entry to the in-process HistoryService so
// get_search_history returns the operator's actual searches organically.
// It is the single hook that wires the production search paths back into
// the HistoryService restored by the restore-runtime-contract change:
// every handler that successfully delegates to a connector calls this
// AFTER the connector returns (so failed connectors do not pollute the
// history). A nil history is treated as no-op so test fixtures that omit
// the HistoryService still work; a nil connector manager is also a no-op
// (handled by the caller not invoking recordSearch in that branch).
//
// The third arg is the connector response so the gate can distinguish
// between three classes of call:
//
//  1. Hard failure (err != nil from the connector): never reaches this
//     helper because every handler bails on err before invoking it.
//  2. Degraded/empty response (Partial=true, Results=[]): skipped. A
//     connector that swallowed an upstream 5xx (e.g. searxng 500 or
//     github 403) returns a response the operator never saw results
//     from — recording it would pollute the audit trail with failed
//     attempts. The corrected gate keeps the operator's history
//     truthful: only completed searches with real (or honestly
//     empty) results appear.
//  3. Successful response (Partial=false): recorded. This covers
//     results-bearing responses AND successful-but-empty searches
//     (Partial stays false because no error occurred); both deserve
//     to appear in the history.
//
// Empty queries are also skipped so accidental whitespace-only inputs
// do not crowd out real searches. Errors from Append are swallowed:
// a failed in-process append MUST NOT bubble up and turn a successful
// search into a 5xx for the AI agent. The history is best-effort
// observability, not part of the contract.
func (s *Server) recordSearch(ctx context.Context, source string, resp *types.SearchResponse) {
	if s.history == nil {
		return
	}
	if resp == nil {
		return
	}
	if resp.Query == "" {
		return
	}
	if resp.Partial && len(resp.Results) == 0 {
		return
	}
	_ = s.history.Append(ctx, cache.Entry{Query: resp.Query, Source: source})
}
