package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mdesantis1984/SourceRudder/internal/connectors"
	"github.com/mdesantis1984/SourceRudder/internal/resultset"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// maxPageSize is the largest page a paging call may ask for.
const maxPageSize = 100

// pageArgs are the paging arguments shared by every search tool. They are
// decoded next to types.SearchRequest so the connector-facing request (and
// therefore every cache key) is unchanged by them.
type pageArgs struct {
	Offset      *int   `json:"offset"`
	ResultSetID string `json:"resultSetId"`
}

// parseSearchArgs decodes the common search arguments and validates paging.
func parseSearchArgs(args json.RawMessage) (types.SearchRequest, pageArgs, error) {
	var req types.SearchRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return req, pageArgs{}, fmt.Errorf("invalid args: %w", err)
	}
	var pg pageArgs
	if err := json.Unmarshal(args, &pg); err != nil {
		return req, pg, fmt.Errorf("invalid args: %w", err)
	}
	if pg.Offset != nil {
		if *pg.Offset < 0 {
			return req, pg, fmt.Errorf("invalid args: offset must be >= 0")
		}
		req.Offset = *pg.Offset
	}
	return req, pg, nil
}

func (p pageArgs) offset() int {
	if p.Offset == nil {
		return 0
	}
	return *p.Offset
}

// SetResultSetStore replaces the store that keeps full result sets. It must
// be called before the server starts serving requests.
func (s *Server) SetResultSetStore(store *resultset.Store) {
	if store != nil {
		s.resultSets = store
	}
}

// sliceResponse returns the slice [offset, offset+limit) of full, plus the
// paging fields. It never reorders results.
func sliceResponse(meta types.SearchResponse, id string, full []types.SearchResultItem, offset, limit int) *types.SearchResponse {
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
	out.Results = append([]types.SearchResultItem{}, full[start:end]...)
	out.ResultSetID = id
	out.Total = total
	out.Offset = offset
	out.HasMore = end < total
	return &out
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
func (s *Server) storeAndPage(limitSource string, req *types.SearchRequest, resp *types.SearchResponse) *types.SearchResponse {
	resp = normalizeSearchResponse(resp)
	if s.resultSets == nil {
		return resp
	}
	meta := *resp
	meta.Results = nil
	meta.ResultSetID, meta.Total, meta.Offset, meta.HasMore = "", 0, 0, false
	id, stored := s.resultSets.Put(resultset.Set{
		Query:        resp.Query,
		DefaultLimit: connectors.ResolveMaxResults(limitSource, 0),
		Meta:         meta,
		Results:      resp.Results,
	})
	out := sliceResponse(meta, id, resp.Results[:stored], req.Offset, connectors.ResolveMaxResults(limitSource, req.MaxResults))
	if stored < len(resp.Results) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("result set truncated: only the first %d of %d results are stored and pageable", stored, len(resp.Results)))
	}
	return out
}

// serveResultSet answers a call that carries resultSetId from the stored set
// without any upstream request.
func (s *Server) serveResultSet(req *types.SearchRequest, pg pageArgs) (*types.SearchResponse, error) {
	var set resultset.Set
	ok := false
	if s.resultSets != nil {
		set, ok = s.resultSets.Get(pg.ResultSetID)
	}
	if !ok {
		return nil, fmt.Errorf("result_set_not_found: %s (expired or unknown; run the search again)", pg.ResultSetID)
	}
	limit := set.DefaultLimit
	if req.MaxResults > 0 {
		limit = req.MaxResults
		if limit > maxPageSize {
			limit = maxPageSize
		}
	}
	out := sliceResponse(set.Meta, pg.ResultSetID, set.Results, pg.offset(), limit)
	if q := strings.TrimSpace(req.Query); q != "" && q != strings.TrimSpace(set.Query) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("query ignored: result set %s was created for query %q; the search was not re-run", pg.ResultSetID, set.Query))
	}
	return normalizeSearchResponse(out), nil
}
