package connectors

import (
	"fmt"
	"strings"

	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

type searxngUnresponsiveError struct {
	engines []searxngUnresponsiveEngine
}

func (e *searxngUnresponsiveError) Error() string {
	engines := make([]string, 0, len(e.engines))
	for _, engine := range e.engines {
		engines = append(engines, fmt.Sprintf("%s: %s", engine.name, engine.reason))
	}
	return fmt.Sprintf("searxng: %d unresponsive engines [%s]", len(e.engines), strings.Join(engines, ", "))
}

func newSearxngUnresponsiveError(entries [][]interface{}) error {
	engines := make([]searxngUnresponsiveEngine, 0, len(entries))
	for _, entry := range entries {
		engine := searxngUnresponsiveEngine{name: "unknown engine", reason: "unknown reason"}
		if len(entry) > 0 {
			if name, ok := entry[0].(string); ok && name != "" {
				engine.name = name
			}
		}
		if len(entry) > 1 {
			if reason, ok := entry[1].(string); ok && reason != "" {
				engine.reason = reason
			}
		}
		engines = append(engines, engine)
	}
	return &searxngUnresponsiveError{engines: engines}
}

type searxngUnresponsiveEngine struct {
	name   string
	reason string
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
//  1. Increments sourcerudder_search_degraded_total{source, kind}.
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
