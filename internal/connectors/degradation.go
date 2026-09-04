package connectors

import (
	"fmt"

	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

type searxngUnresponsiveError struct {
	engines []string
}

func (e *searxngUnresponsiveError) Error() string {
	return fmt.Sprintf("searxng: %d unresponsive engines %v: timeout", len(e.engines), e.engines)
}

func newSearxngUnresponsiveError(entries [][]interface{}) error {
	engines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if len(entry) > 0 {
			if name, ok := entry[0].(string); ok {
				engines = append(engines, name)
			}
		}
	}
	return &searxngUnresponsiveError{engines: engines}
}

func recordSearxngError(source string, results []types.SearchResultItem, err error, resp *types.SearchResponse) {
	if err == nil {
		return
	}
	if _, ok := err.(*searxngUnresponsiveError); ok {
		recordDegraded(source, "unresponsive_engines", resp, err)
		return
	}
	if len(results) == 0 {
		resp.Partial = true
		resp.Warnings = append(resp.Warnings, err.Error())
	}
}

// recordDegraded is the single seam every connector uses to surface an
// upstream degradation. It does three things atomically:
//  1. Increments ia_buscar_search_degraded_total{source, kind}.
//  2. Marks resp.Partial = true so the AI can tell the result was
//     incomplete without parsing Warnings.
//  3. Appends err.Error() to resp.Warnings without clobbering any
//     warning a caller already attached.
//
// Centralising this prevents drift between the metric labels, the
// Partial flag, and the Warnings slice; it is also the only knob the
// Phase 4 anonymous-reddit delivery needs to keep the existing
// degradation contract intact after the OAuth removal.
func recordDegraded(source, kind string, resp *types.SearchResponse, err error) {
	if resp == nil {
		return
	}
	observability.Default().RecordSearchDegraded(source, kind)
	resp.Partial = true
	if err != nil {
		resp.Warnings = append(resp.Warnings, err.Error())
	}
}
