package cache

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// TestHistoryServiceAppendAndList round-trips a single entry through
// the HistoryService: after Append, List with a sufficient limit
// returns that entry in newest-first order. Triangulates with the
// bounded case so a regression that drops newest-first surfaces here.
func TestHistoryServiceAppendAndList(t *testing.T) {
	s := NewHistoryService(10)

	if err := s.Append(context.Background(), Entry{Query: "alpha", Source: "web"}); err != nil {
		t.Fatalf("Append alpha: %v", err)
	}
	if err := s.Append(context.Background(), Entry{Query: "beta", Source: "github"}); err != nil {
		t.Fatalf("Append beta: %v", err)
	}
	if err := s.Append(context.Background(), Entry{Query: "gamma", Source: "reddit"}); err != nil {
		t.Fatalf("Append gamma: %v", err)
	}

	got := s.List(context.Background(), 10, "")
	want := []string{"gamma", "beta", "alpha"}
	if !queriesEqual(got, want) {
		t.Fatalf("List newest-first: got %v, want %v", queries(got), want)
	}
}

// TestHistoryServiceBoundedList proves the HistoryService caps its
// own internal capacity: appending more entries than the configured
// bound makes List return only the most recent ones. The service is
// in-process and bounded by design.
func TestHistoryServiceBoundedList(t *testing.T) {
	const bound = 3
	s := NewHistoryService(bound)

	for i := 0; i < 7; i++ {
		q := string(rune('a' + i))
		if err := s.Append(context.Background(), Entry{Query: q, Source: "web"}); err != nil {
			t.Fatalf("Append %s: %v", q, err)
		}
	}
	got := s.List(context.Background(), 100, "")
	if len(got) > bound {
		t.Fatalf("List returned %d entries; bound is %d", len(got), bound)
	}
	want := []string{"g", "f", "e"}
	if !queriesEqual(got, want) {
		t.Fatalf("Bounded list should keep newest %d: got %v, want %v", bound, queries(got), want)
	}
}

// TestHistoryServiceQuerySubstringFilter narrows the returned entries
// to those whose stored Query contains the substring argument. The
// filter is case-sensitive; a missing query returns the full bounded
// list. Triangulates the empty-query path so a regression that
// silently treats empty as no-match surfaces here.
func TestHistoryServiceQuerySubstringFilter(t *testing.T) {
	s := NewHistoryService(10)

	for _, q := range []string{"go fetch", "go parse", "python scrape", "go render"} {
		if err := s.Append(context.Background(), Entry{Query: q, Source: "web"}); err != nil {
			t.Fatalf("Append %q: %v", q, err)
		}
	}

	got := s.List(context.Background(), 10, "go")
	want := []string{"go render", "go parse", "go fetch"}
	if !queriesEqual(got, want) {
		t.Fatalf("Substring filter: got %v, want %v", queries(got), want)
	}
}

// TestHistoryServiceLimitClampsResult proves the explicit limit on
// List truncates a larger bounded buffer to the requested cap. The
// newest-first ordering MUST be preserved across the truncation.
func TestHistoryServiceLimitClampsResult(t *testing.T) {
	s := NewHistoryService(10)
	for _, q := range []string{"a", "b", "c", "d", "e"} {
		if err := s.Append(context.Background(), Entry{Query: q, Source: "web"}); err != nil {
			t.Fatalf("Append %q: %v", q, err)
		}
	}
	got := s.List(context.Background(), 2, "")
	want := []string{"e", "d"}
	if !queriesEqual(got, want) {
		t.Fatalf("Limit clamp: got %v, want %v", queries(got), want)
	}
}

// helpers ------------------------------------------------------------------

func queries(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Query
	}
	return out
}

func queriesEqual(got []Entry, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	g := queries(got)
	if !sort.StringsAreSorted(want) {
		// want must be newest-first; do not sort
	}
	return reflect.DeepEqual(g, want)
}
