package observability

import (
	"strings"
	"testing"
)

// TestRecordSearchDegradedIncrementsCounter locks in the contract that
// the new ia_buscar_search_degraded_total counter increments whenever a
// connector surfaces upstream degradation. The exact label values are
// part of the public observability surface, so a future regression that
// renames them would break dashboards.
func TestRecordSearchDegradedIncrementsCounter(t *testing.T) {
	m := New()

	m.RecordSearchDegraded("web", "unresponsive_engines")
	m.RecordSearchDegraded("web", "unresponsive_engines")
	m.RecordSearchDegraded("youtube", "unresponsive_engines")
	m.RecordSearchDegraded("reddit", "rate_limited")
	m.RecordSearchDegraded("reddit", "unconfigured")

	body := scrapeCounter(t, m.Handler(), "ia_buscar_search_degraded_total")

	expectations := []struct {
		labels string
		want   string
	}{
		{`source="web",kind="unresponsive_engines"`, "2"},
		{`source="youtube",kind="unresponsive_engines"`, "1"},
		{`source="reddit",kind="rate_limited"`, "1"},
		{`source="reddit",kind="unconfigured"`, "1"},
	}
	for _, exp := range expectations {
		// Prometheus sorts label names alphabetically, so the on-wire
		// order is kind=...,source=... . Accept either order so the
		// test does not break if the prom client reorders again.
		altLabels := reverseLabelOrder(exp.labels)
		line := findMetricLine(body, exp.labels)
		if line == "" {
			line = findMetricLine(body, altLabels)
		}
		if line == "" {
			t.Errorf("expected a sample line with labels %q (or %q), got none in body:\n%s", exp.labels, altLabels, body)
			continue
		}
		if !strings.HasSuffix(strings.TrimSpace(line), exp.want) {
			t.Errorf("expected counter %s to end with %q, got %q", exp.labels, exp.want, strings.TrimSpace(line))
		}
	}
}

// TestRecordSearchDegradedDefaultsUnknownLabels ensures the helper does
// not panic or emit empty label values, which would break Prometheus
// scrapers.
func TestRecordSearchDegradedDefaultsUnknownLabels(t *testing.T) {
	m := New()
	m.RecordSearchDegraded("", "")
	m.RecordSearchDegraded("only-source", "")

	body := scrapeCounter(t, m.Handler(), "ia_buscar_search_degraded_total")
	if !strings.Contains(body, `source="unknown"`) {
		t.Errorf("expected source=\"unknown\" label fallback, got:\n%s", body)
	}
	if !strings.Contains(body, `kind="unknown"`) {
		t.Errorf("expected kind=\"unknown\" label fallback, got:\n%s", body)
	}
	if !strings.Contains(body, `source="only-source"`) {
		t.Errorf("expected non-empty source label to round-trip, got:\n%s", body)
	}
}

// reverseLabelOrder swaps "a=\"x\",b=\"y\"" into "b=\"y\",a=\"x\"" so the
// helper accepts both label orderings. Prometheus emits labels sorted
// alphabetically regardless of how we registered the metric, so any
// caller-facing label list may need to be re-ordered before comparison.
func reverseLabelOrder(s string) string {
	// Find the boundary between the two key="value" pairs by looking
	// for the comma that sits between them, but ignore commas inside
	// quoted values (we never use quoted commas in label values).
	var out strings.Builder
	inQuote := false
	for i, r := range s {
		switch r {
		case '"':
			inQuote = !inQuote
			out.WriteRune(r)
		case ',':
			if inQuote {
				out.WriteRune(r)
			} else {
				// Splice in a sentinel we can split on, mark position.
				if i == len(s)-1 {
					out.WriteRune(r)
				} else {
					out.WriteString("|SPLIT|")
				}
			}
		default:
			out.WriteRune(r)
		}
	}
	parts := strings.Split(out.String(), "|SPLIT|")
	if len(parts) != 2 {
		return s
	}
	return parts[1] + "," + parts[0]
}

// TestDefaultMetricsIsProcessShared locks in that the package-level
// Default() returns the same instance across calls, so connectors that
// fire RecordSearchDegraded from goroutines hit the same counter.
func TestDefaultMetricsIsProcessShared(t *testing.T) {
	SetDefault(New())
	a := Default()
	b := Default()
	if a != b {
		t.Fatalf("Default() must return the same instance across calls, got %p vs %p", a, b)
	}
	a.RecordSearchDegraded("web", "unresponsive_engines")

	SetDefault(New())
	c := Default()
	if c == a {
		t.Fatalf("SetDefault must replace the previous default")
	}
	if c == b {
		t.Fatalf("SetDefault must replace the previous default")
	}
}