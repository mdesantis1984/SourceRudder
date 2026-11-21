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
func parseSearchArgs(args json.RawMessage) (types.SearchRequest, pageArgs, error) {
	var req types.SearchRequest
	if err := json.Unmarshal(args, &req); err != nil {
		return req, pageArgs{}, fmt.Errorf("invalid args: %w", err)
	}
	// format only selects the text rendering; it never reaches a connector
	// (or a cache key).
	req.Format = ""
	var pg pageArgs
	if err := json.Unmarshal(args, &pg); err != nil {
		return req, pg, fmt.Errorf("invalid args: %w", err)
	}
	if _, _, err := parseRender(args); err != nil {
		return req, pg, err
	}
	if pg.Cursor != "" {
		id, offset, err := decodeCursor(pg.Cursor)
		if err != nil {
			return req, pg, fmt.Errorf("invalid_cursor: %w", err)
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
	out.NextCursor = ""
	if end < total {
		out.NextCursor = encodeCursor(id, end)
	}
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
	meta.ResultSetID, meta.Total, meta.Offset, meta.NextCursor = "", 0, 0, ""
	id, stored := s.resultSets.Put(resultset.Set{
		Query:        resp.Query,
		DefaultLimit: connectors.ResolveMaxResults(limitSource, 0),
		Meta:         meta,
		Results:      resp.Results,
	})
	out := sliceResponse(meta, id, resp.Results[:stored], 0, connectors.ResolveMaxResults(limitSource, req.MaxResults))
	if stored < len(resp.Results) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("result set truncated: only the first %d of %d results are stored and pageable", stored, len(resp.Results)))
	}
	return out
}

// serveResultSet answers a call that carries a cursor from the stored set
// without any upstream request.
func (s *Server) serveResultSet(req *types.SearchRequest, pg pageArgs) (*types.SearchResponse, error) {
	var set resultset.Set
	ok := false
	if s.resultSets != nil {
		set, ok = s.resultSets.Get(pg.setID)
	}
	if !ok {
		return nil, fmt.Errorf("result_set_not_found: %s (expired or unknown; run the search again)", pg.setID)
	}
	limit := set.DefaultLimit
	if req.MaxResults > 0 {
		limit = req.MaxResults
		if limit > maxPageSize {
			limit = maxPageSize
		}
	}
	out := sliceResponse(set.Meta, pg.setID, set.Results, pg.offset, limit)
	if q := strings.TrimSpace(req.Query); q != "" && q != strings.TrimSpace(set.Query) {
		out.Warnings = append(out.Warnings, fmt.Sprintf("query ignored: result set %s was created for query %q; the search was not re-run", pg.setID, set.Query))
	}
	return normalizeSearchResponse(out), nil
}
