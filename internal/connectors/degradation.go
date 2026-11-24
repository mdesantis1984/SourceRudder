package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/cache"
	"github.com/mdesantis1984/SourceRudder/internal/observability"
	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

// DegradedSearchCacheTTL bounds stale degradation while avoiding retry storms.
const DegradedSearchCacheTTL = 30 * time.Second

func cacheSearxngResponse(ctx context.Context, svc *cache.Service, key string, resp *types.SearchResponse) {
	if svc == nil {
		return
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	var cap time.Duration
	// Partial responses and empty responses (even healthy ones) are cached
	// briefly so a transient miss is not served for the full TTL.
	if resp.Partial || len(resp.Results) == 0 {
		cap = DegradedSearchCacheTTL
	}
	svc.SetWithMaxTTL(ctx, key, data, resp.SourcesUsed, cap)
}

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

// TimeRangeEmptyWarning explains an empty answer to a search that carried
// timeRange: SearXNG drops every engine that cannot filter by date (searxng
// issue 6957), so undated results can disappear without any engine failing.
const TimeRangeEmptyWarning = "timeRange: SearXNG skips engines without date filtering; retry without timeRange for undated results"

// recordTimeRangeEmpty appends TimeRangeEmptyWarning when the request set
// timeRange and the search returned no results. It only adds the warning: it
// never retries and never marks the response partial. A hard failure (request
// error, HTTP error, decode error) is not blamed on timeRange; unresponsive
// engines and a failed page 2 still are, because the engines that did answer
// returned nothing.
func recordTimeRangeEmpty(req *types.SearchRequest, resp *types.SearchResponse, err error) {
	if req == nil || resp == nil || req.TimeRange == "" || len(resp.Results) != 0 {
		return
	}
	if err != nil {
		switch err.(type) {
		case *searxngUnresponsiveError, *webPageTwoError:
		default:
			return
		}
	}
	resp.Warnings = append(resp.Warnings, TimeRangeEmptyWarning)
}
