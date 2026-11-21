package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/mdesantis1984/SourceRudder/internal/compact"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// Text formats a search tool can render its result in.
const (
	formatJSON    = "json"
	formatCompact = "compact"
)

// renderArgs are the optional rendering arguments of the search tools.
type renderArgs struct {
	Format string `json:"format"`
	Detail string `json:"detail"`
}

// parseRender validates `format` and `detail` and returns their effective
// values. Empty values select the defaults (json, snippets). An unknown value
// is an argument error naming the allowed ones.
func parseRender(args json.RawMessage) (string, compact.Detail, error) {
	var ra renderArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &ra); err != nil {
			return "", "", fmt.Errorf("invalid args: %w", err)
		}
	}
	format := ra.Format
	switch format {
	case "":
		format = formatJSON
	case formatJSON, formatCompact:
	default:
		return "", "", fmt.Errorf("invalid args: format %q is not allowed: allowed values are %q, %q", ra.Format, formatJSON, formatCompact)
	}
	detail, err := compact.ParseDetail(ra.Detail)
	if err != nil {
		return "", "", fmt.Errorf("invalid args: %w", err)
	}
	return format, detail, nil
}

// toolResult builds the tools/call result for every call path:
//
//	{"content":[{"type":"text","text":...}], "structuredContent": <object>}
//
// structuredContent is the full typed value, emitted only when it marshals to
// a JSON object (MCP 2025-11-25). The text block mirrors it as minified JSON
// by default, or as compact text when a search tool was called with
// format=compact; it is never empty.
func toolResult(args json.RawMessage, v interface{}) (map[string]interface{}, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode result: %w", err)
	}
	text := string(body)
	if sr := asSearchResponse(v); sr != nil {
		format, detail, err := parseRender(args)
		if err != nil {
			return nil, err
		}
		if format == formatCompact {
			text = compact.Format(*sr, compact.Page{
				ResultSetID: sr.ResultSetID,
				Total:       sr.Total,
				Offset:      sr.Offset,
				NextCursor:  sr.NextCursor,
			}, detail)
		}
	}
	if text == "" {
		text = "null"
	}
	res := map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
	}
	if trimmed := bytes.TrimSpace(body); len(trimmed) > 0 && trimmed[0] == '{' {
		res["structuredContent"] = json.RawMessage(trimmed)
	}
	return res, nil
}

func asSearchResponse(v interface{}) *types.SearchResponse {
	switch r := v.(type) {
	case *types.SearchResponse:
		return r
	case types.SearchResponse:
		return &r
	}
	return nil
}

// appendWarning adds a warning without ever writing into the spare capacity
// of the slice it was given (a connector or the response cache may own it).
func appendWarning(warnings []string, w string) []string {
	return append(slices.Clip(slices.Clone(warnings)), w)
}

// errorResult is the tools/call result of a failed tool execution:
// {"content":[{"type":"text","text":<message>}],"isError":true}, without
// structuredContent. The text is never empty, because clients substitute a
// generic message for an empty one.
func errorResult(err error) map[string]interface{} {
	text := err.Error()
	if text == "" {
		text = "tool execution failed"
	}
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": text}},
		"isError": true,
	}
}

// usesRetiredPagingArgs reports whether a search call still carries offset or
// resultSetId, the arguments that cursor paging replaced. Synthesis tools
// accept resultSetId legitimately and never reach this check.
func usesRetiredPagingArgs(args json.RawMessage) bool {
	var keys map[string]json.RawMessage
	if json.Unmarshal(args, &keys) != nil {
		return false
	}
	_, offset := keys["offset"]
	_, setID := keys["resultSetId"]
	return offset || setID
}

// withRetiredArgsWarning adds the replacement warning to a search response
// without writing into storage the response may share.
func withRetiredArgsWarning(v interface{}) interface{} {
	sr := asSearchResponse(v)
	if sr == nil {
		return v
	}
	cp := *sr
	cp.Warnings = appendWarning(cp.Warnings, retiredArgsWarning)
	return &cp
}
