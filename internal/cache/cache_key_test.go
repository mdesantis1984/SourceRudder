package cache

import (
	"strings"
	"testing"
)

// TestGenerateCacheKeyIncludesTimeRange is the Phase 3.5 RED gate. The
// cache key MUST vary when only the time-range axis differs; otherwise
// the same query for "today" and "week" returns stale, wrong-time
// results. The signature change to add timeRange must keep backward
// compatibility with callers that pass no time range.
func TestGenerateCacheKeyIncludesTimeRange(t *testing.T) {
	noRange := GenerateCacheKey("kubernetes", []string{"web"}, "")
	dayRange := GenerateCacheKey("kubernetes", []string{"web"}, "day")
	weekRange := GenerateCacheKey("kubernetes", []string{"web"}, "week")

	if noRange == dayRange {
		t.Fatalf("expected key to vary when TimeRange changes from \"\" to \"day\", got identical %q", noRange)
	}
	if dayRange == weekRange {
		t.Fatalf("expected key to vary between \"day\" and \"week\" ranges, got identical %q", dayRange)
	}
	if !strings.HasPrefix(noRange, "cache:") {
		t.Fatalf("expected generated keys to keep the 'cache:' prefix for consistency, got %q", noRange)
	}
}

// TestGenerateCacheKeyStableForSameInputs is the triangulation case:
// the helper is a pure function; same inputs must yield the same key.
func TestGenerateCacheKeyStableForSameInputs(t *testing.T) {
	a := GenerateCacheKey("docker", []string{"dockerhub"}, "month")
	b := GenerateCacheKey("docker", []string{"dockerhub"}, "month")
	if a != b {
		t.Fatalf("expected identical inputs to yield identical keys, got %q vs %q", a, b)
	}
}

// TestGenerateCacheKeyDistinguishesSources is the triangulation case
// across source-axis changes; same query but different source slice
// must produce different keys.
func TestGenerateCacheKeyDistinguishesSources(t *testing.T) {
	web := GenerateCacheKey("rust", []string{"web"}, "")
	images := GenerateCacheKey("rust", []string{"images"}, "")
	if web == images {
		t.Fatalf("expected different source slices to yield different keys, both got %q", web)
	}
}
