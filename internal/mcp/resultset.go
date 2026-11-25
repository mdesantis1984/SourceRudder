package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mdesantis1984/SourceRudder/internal/connectors"
	"github.com/mdesantis1984/SourceRudder/internal/resultfilter"
	"github.com/mdesantis1984/SourceRudder/internal/resultset"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// maxPageSize is the largest page a paging call may ask for.
const maxPageSize = 100

// pageArgs are the paging and rendering arguments shared by every search
// tool. They are decoded next to types.SearchRequest so the connector-facing
// request (and therefore every cache key) is unchanged by them.
type pageArgs struct {
	// Cursor is the nextCursor of a previous response; setID and offset are
	// its validated contents.
	Cursor string `json:"cursor"`
	setID  string
	offset int
}

// parseSearchArgs decodes the common search arguments and validates paging
// and rendering (format/detail) before any search or store access happens.
func parseSearchArgs(ctx context.Context, args json.RawMessage) (types.SearchRequest, pageArgs, error) {
	var req types.SearchRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return req, pageArgs{}, fmt.Errorf("invalid args: %w", err)
	}
	// format only selects the text rendering; it never reaches a connector
	// (or a cache key).
	req.Format = ""
	// safeSearch defaults to true (moderate) when the caller omits it; an
	// explicit false keeps it off. Presence is read from the raw arguments
	// because the request struct cannot tell false from absent.
	var safe struct {
		SafeSearch *bool `json:"safeSearch"`
	}
	if err := json.Unmarshal(args, &safe); err != nil {
		return req, pageArgs{}, fmt.Errorf("invalid args: %w", err)
	}
	req.SafeSearch = safe.SafeSearch == nil || *safe.SafeSearch
	var pg pageArgs
	if err := json.Unmarshal(args, &pg); err != nil {
		return req, pg, fmt.Errorf("invalid args: %w", err)
	}
	if _, _, err := parseRender(args); err != nil {
		return req, pg, err
	}
	if pg.Cursor == "" && strings.TrimSpace(req.Query) == "" {
		return req, pg, fmt.Errorf("invalid_arguments: provide query or cursor")
	}
	if pg.Cursor != "" {
		issuer, id, offset, err := decodeCursor(pg.Cursor)
		if err != nil {
			return req, pg, fmt.Errorf("invalid_cursor: %w", err)
		}
		if tool := toolNameFrom(ctx); tool != "" && issuer != tool {
			return req, pg, fmt.Errorf("invalid_cursor: cursor was issued by %s", issuer)
		}
		pg.setID, pg.offset = id, offset
	}
	return req, pg, nil
}

// SetResultSetStore replaces the store that keeps full result sets. It must
// be called before the server starts serving requests.
func (s *Server) SetResultSetStore(store *resultset.Store) {
	if store != nil {
		s.resultSets = store
	}
}

// webPager is the optional capability of the web connector that lets a set
// grow by one SearXNG page when a caller pages past its end.
type webPager interface {
	NextPage(ctx context.Context, query string, pageno int, req *types.SearchRequest, seen map[string]struct{}, lastScore float64) ([]types.SearchResultItem, error)
}

func (s *Server) webPager() webPager {
	if s.connectorManager == nil {
		return nil
	}
	conn, ok := s.connectorManager.GetConnector("web")
	if !ok {
		return nil
	}
	p, _ := conn.(webPager)
	return p
}

// canExtend reports whether a set can still grow past its stored results.
func (s *Server) canExtend(c *resultset.Continuation) bool {
	return c != nil && !c.Done && c.NextPage >= 2 && c.NextPage <= connectors.MaxSearxngPages && s.webPager() != nil
}

// extendSet fetches one more upstream page for a set whose stored results end
// at or before offset. Concurrent callers share one fetch. A failed fetch
// leaves the set unchanged. The returned notes are response warnings: what
// the result filters removed from the new page, or why the fetch failed.
func (s *Server) extendSet(ctx context.Context, id string, offset int) (resultset.Set, bool, []string) {
	pager := s.webPager()
	var failure string
	var removed []string
	set, ok, _ := s.resultSets.Extend(id, offset, func(cont resultset.Continuation, have []types.SearchResultItem) ([]types.SearchResultItem, resultset.Continuation, error) {
		seen := make(map[string]struct{}, len(have))
		for _, it := range have {
			seen[it.URL] = struct{}{}
		}
		last := 0.0
		if len(have) > 0 {
			last = have[len(have)-1].Score
		}
		req := &types.SearchRequest{Language: cont.Language, TimeRange: cont.TimeRange, SafeSearch: cont.SafeSearch}
		more, err := pager.NextPage(ctx, cont.Query, cont.NextPage, req, seen, last)
		if err != nil {
			failure = fmt.Sprintf("web page %d could not be fetched; the stored results are unchanged and the cursor can be retried: %v", cont.NextPage, err)
			return nil, cont, err
		}
		// The upstream page is judged by what it returned, so Done keeps
		// meaning "the engines have nothing more" even when every result of
		// this page is filtered out.
		fetched := len(more)
		var noHost, adult int
		more, noHost, adult = filterResults("web", cont.SafeSearch, more)
		removed = append(removed, resultfilter.Warnings(noHost, adult)...)
		next := cont
		next.NextPage++
		if fetched == 0 || next.NextPage > connectors.MaxSearxngPages {
			next.Done = true
		}
		return more, next, nil
	})
	if failure != "" {
		removed = append(removed, failure)
	}
	return set, ok, removed
}

// sliceResponse returns the slice [offset, offset+limit) of full, plus the
// paging fields. It never reorders results. more says the set can still grow
// upstream, which keeps nextCursor present at the end of the stored results.
func sliceResponse(tool string, meta types.SearchResponse, id string, full []types.SearchResultItem, offset, limit int, more bool) *types.SearchResponse {
	total := len(full)
	start := offset
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	out := meta
	// meta may alias slices owned by a connector or the response cache;
	// clip-clone them so appending a warning never writes into shared storage.
	out.KeyFindings = cloneStrings(meta.KeyFindings)
	out.SourcesUsed = cloneStrings(meta.SourcesUsed)
	out.Warnings = cloneStrings(meta.Warnings)
	out.Errors = cloneStrings(meta.Errors)
	out.Answers = cloneStrings(meta.Answers)
	out.Suggestions = cloneStrings(meta.Suggestions)
	out.Corrections = cloneStrings(meta.Corrections)
	out.Infoboxes = slices.Clip(slices.Clone(meta.Infoboxes))
	out.Results = append([]types.SearchResultItem{}, full[start:end]...)
	out.ResultSetID = id
	out.Total = total
	out.Offset = offset
	out.SearxngPages = 0
	out.NextCursor = ""
	if end < total || more {
		out.NextCursor = encodeCursor(tool, id, end)
	}
	return &out
}

// filterResults applies the host and adult-domain filters. The local index
// serves its own local-index:// URLs, so its URLs are not required to be
// http(s); every other source is.
func filterResults(source string, safeSearch bool, items []types.SearchResultItem) ([]types.SearchResultItem, int, int) {
	if source == "local_index" {
		if !safeSearch {
			return items, 0, 0
		}
		kept, adult := resultfilter.FilterAdult(items)
		return kept, 0, adult
	}
	return resultfilter.Filter(items, safeSearch)
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return slices.Clip(slices.Clone(in))
}

// storeAndPage keeps the full result list of a completed search under an
// opaque ID and returns the requested slice. limitSource selects the page-size
// rules (search_web defaults to 20, the rest to 10).
func (s *Server) storeAndPage(ctx context.Context, limitSource string, req *types.SearchRequest, resp *types.SearchResponse) *types.SearchResponse {
	pages := 0
	if resp != nil {
		pages = resp.SearxngPages
		cp := *resp // never mutate a response a connector or cache may share
		cp.SearxngPages = 0
		resp = &cp
	}
	resp = normalizeSearchResponse(resp)
	// Results with no usable host and, with safe search on, adult domains
	// never reach a caller, whichever tool or cache produced them.
	fetched := len(resp.Results)
	var noHost, adult int
	resp.Results, noHost, adult = filterResults(limitSource, req.SafeSearch, resp.Results)
	filterWarnings := resultfilter.Warnings(noHost, adult)
	resp.Warnings = append(slices.Clip(resp.Warnings), filterWarnings...)
	if s.resultSets == nil {
		return resp
	}
	meta := *resp
	meta.Results = nil
	meta.ResultSetID, meta.Total, meta.Offset, meta.NextCursor = "", 0, 0, ""
	set := resultset.Set{
		Query: resp.Query,
		// The first page's size is kept for the cursor pages; a maxResults
		// sent with a cursor still overrides it for that call.
		DefaultLimit: min(connectors.ResolveMaxResults(limitSource, req.MaxResults), maxPageSize),
		Meta:         meta,
		Results:      resp.Results,
	}
	// Only a web search that read SearXNG pages can grow lazily; the request
	// context is kept so the next page matches the original search.
	if limitSource == "web" && pages > 0 && fetched > 0 && len(resp.Results) <= s.resultSets.Config().MaxResults {
		set.Continuation = &resultset.Continuation{
			Query:      strings.TrimSpace(req.Query),
			Language:   req.Language,
			TimeRange:  req.TimeRange,
			SafeSearch: req.SafeSearch,
			NextPage:   pages + 1,
		}
	}
	id, stored := s.resultSets.Put(set)
	out := sliceResponse(toolNameFrom(ctx), meta, id, resp.Results[:stored], 0, connectors.ResolveMaxResults(limitSource, req.MaxResults), s.canExtend(set.Continuation))
	if stored < len(resp.Results) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("result set truncated: only the first %d of %d results are stored and pageable", stored, len(resp.Results)))
	}
	return out
}

// serveResultSet answers a call that carries a cursor from the stored set
// without any upstream request.
func (s *Server) serveResultSet(ctx context.Context, req *types.SearchRequest, pg pageArgs) (*types.SearchResponse, error) {
	var set resultset.Set
	ok := false
	if s.resultSets != nil {
		set, ok = s.resultSets.Get(pg.setID)
	}
	if !ok {
		return nil, fmt.Errorf("result_set_not_found: %s (expired or unknown; run the search again)", pg.setID)
	}
	// Past the end of a web set that may have more upstream: fetch exactly
	// one more SearXNG page (shared by concurrent callers).
	var extra []string
	if pg.offset >= len(set.Results) && s.canExtend(set.Continuation) {
		var found bool
		var notes []string
		set, found, notes = s.extendSet(ctx, pg.setID, pg.offset)
		if !found {
			return nil, fmt.Errorf("result_set_not_found: %s (expired or unknown; run the search again)", pg.setID)
		}
		extra = append(extra, notes...)
		if set.Continuation != nil && set.Continuation.BudgetHit {
			extra = append(extra, "result set full: the result budget stopped further web pages")
		}
	}
	limit := set.DefaultLimit
	if req.MaxResults > 0 {
		limit = req.MaxResults
		if limit > maxPageSize {
			limit = maxPageSize
		}
	}
	out := sliceResponse(toolNameFrom(ctx), set.Meta, pg.setID, set.Results, pg.offset, limit, s.canExtend(set.Continuation))
	out.Warnings = append(out.Warnings, extra...)
	if q := strings.TrimSpace(req.Query); q != "" && q != strings.TrimSpace(set.Query) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("query ignored: result set %s was created for query %q; the search was not re-run", pg.setID, set.Query))
	}
	return normalizeSearchResponse(out), nil
}
