package mcp

import (
	"strings"
	"testing"
	"time"

	"github.com/mdesantis1984/SourceRudder/internal/resultset"
)

func TestResultSetExpiredIDIsAToolErrorAndLeavesStoreUnchanged(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	fake := &fakeResultsConnector{name: "web", n: 12}
	s := newResultSetServer(t, fake)
	s.SetResultSetStore(resultset.New(resultset.Config{TTL: time.Minute, MaxSets: 4, MaxResults: 100, Now: func() time.Time { return now }}))

	first := mustPaged(t, s, "search_web", map[string]interface{}{"query": "q", "maxResults": 5})
	now = now.Add(2 * time.Minute)
	before := s.resultSets.Stats()

	_, err := callPaged(t, s, "search_web", map[string]interface{}{"query": "q", "offset": 5, "resultSetId": first.ResultSetID})
	want := "result_set_not_found: " + first.ResultSetID + " (expired or unknown; run the search again)"
	if err == nil || err.Error() != want {
		t.Fatalf("expired id error = %v, want %q", err, want)
	}
	if after := s.resultSets.Stats(); after != before {
		t.Fatalf("a failed lookup changed the store: before=%+v after=%+v", before, after)
	}
	if fake.callCount() != 1 {
		t.Fatalf("expired id must not re-run the search; calls=%d", fake.callCount())
	}
}

func TestResultSetStoreBoundsApplyThroughTheHandler(t *testing.T) {
	fake := &fakeResultsConnector{name: "web", n: 3}
	s := newResultSetServer(t, fake)
	s.SetResultSetStore(resultset.New(resultset.Config{TTL: time.Hour, MaxSets: 2, MaxResults: 100}))
	var ids []string
	for _, q := range []string{"a", "b", "c"} {
		ids = append(ids, mustPaged(t, s, "search_web", map[string]interface{}{"query": q, "maxResults": 1}).ResultSetID)
	}
	if st := s.resultSets.Stats(); st.Sets != 2 {
		t.Fatalf("sets=%d want 2", st.Sets)
	}
	if _, err := callPaged(t, s, "search_web", map[string]interface{}{"query": "a", "resultSetId": ids[0]}); err == nil || !strings.HasPrefix(err.Error(), "result_set_not_found:") {
		t.Fatalf("oldest set must be evicted, got %v", err)
	}
	if _, err := callPaged(t, s, "search_web", map[string]interface{}{"query": "c", "resultSetId": ids[2]}); err != nil {
		t.Fatalf("newest set must survive: %v", err)
	}
}
