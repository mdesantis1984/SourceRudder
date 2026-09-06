package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"

	"github.com/thiscloud/ia-buscar/internal/cache"
	"github.com/thiscloud/ia-buscar/internal/observability"
	"github.com/thiscloud/ia-buscar/pkg/types"
)

// makeSearxngServer returns an httptest server that responds with the
// JSON payload every connector in this package expects. Pass an empty
// results slice to force the unresponsive_engines branch; pass data
// to test the happy path.
func makeSearxngServer(t *testing.T, payload string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func searxngImagesOK() string {
	return `{"results":[{"title":"A","url":"https://a.example","content":"snippet","img_src":"https://a.example/img.png","engine":"bing images","parsed_url":{"domain":"a.example"}}],"unresponsive_engines":[]}`
}

func searxngImagesDegraded() string {
	return `{"results":[],"unresponsive_engines":[["bing images"]]}`
}

func TestSearxngUnresponsiveErrorPreservesReasons(t *testing.T) {
	tests := []struct {
		name    string
		entries [][]interface{}
		want    string
	}{
		{
			name: "heterogeneous reasons survive in upstream order",
			entries: [][]interface{}{
				{"brave", "too many requests"},
				{"google", "Suspended: CAPTCHA"},
				{"duckduckgo", "Suspended: timeout"},
			},
			want: "searxng: 3 unresponsive engines [brave: too many requests, google: Suspended: CAPTCHA, duckduckgo: Suspended: timeout]",
		},
		{
			name: "no timeout is fabricated",
			entries: [][]interface{}{
				{"brave", "too many requests"},
				{"google", "Suspended: CAPTCHA"},
			},
			want: "searxng: 2 unresponsive engines [brave: too many requests, google: Suspended: CAPTCHA]",
		},
		{
			name: "malformed data uses deterministic fallbacks",
			entries: [][]interface{}{
				{"missing reason"},
				{"", ""},
				{42, "too many requests"},
				{},
				{"nil reason", nil},
			},
			want: "searxng: 5 unresponsive engines [missing reason: unknown reason, unknown engine: unknown reason, unknown engine: too many requests, unknown engine: unknown reason, nil reason: unknown reason]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newSearxngUnresponsiveError(tt.entries).Error(); got != tt.want {
				t.Fatalf("unexpected warning:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

// TestImagesConnectorBodyWithin50ms is the Phase 3.1 RED gate. The
// images connector currently sleeps 500ms before its HTTP call; this
// test asserts the request path runs in under 50ms. It must fail
// against the unmodified images.go and pass once the time.Sleep call
// is removed (Phase 3.2 GREEN).
func TestImagesConnectorBodyWithin50ms(t *testing.T) {
	srv := makeSearxngServer(t, searxngImagesOK())
	c := NewImagesConnector(srv.URL, cache.NewService(60))

	start := time.Now()
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "x"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected at least one result from httptest server")
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("images.Search took %v; spec requires <50ms body latency (Phase 3.2 removes the artificial sleep)", elapsed)
	}
}

// TestNewsConnectorBodyWithin50ms covers a second 500ms-sleep source
// to triangulate the Phase 3.2 GREEN behavior across sources.
func TestNewsConnectorBodyWithin50ms(t *testing.T) {
	srv := makeSearxngServer(t, `{"results":[{"title":"N","url":"https://n.example","content":"snippet","engine":"bing news"}]}`)
	c := NewNewsConnector(srv.URL, cache.NewService(60))

	start := time.Now()
	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "x"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatalf("expected at least one result from httptest server")
	}
	if elapsed > 50*time.Millisecond {
		t.Fatalf("news.Search took %v; spec requires <50ms body latency", elapsed)
	}
}

// TestRecordDegradedIncrementsMetricAndSetsPartial is the Phase 3.3
// RED gate. The helper must do THREE things atomically:
//  1. increment ia_buscar_search_degraded_total{source, kind}
//  2. set resp.Partial = true
//  3. append err.Error() to resp.Warnings
func TestRecordDegradedIncrementsMetricAndSetsPartial(t *testing.T) {
	m := observability.New()
	observability.SetDefault(m)

	resp := &types.SearchResponse{}
	recordDegraded("web", "unresponsive_engines", resp, errSample("timeout on bing"))

	if !resp.Partial {
		t.Fatalf("expected resp.Partial=true after recordDegraded")
	}
	if len(resp.Warnings) != 1 || resp.Warnings[0] != "timeout on bing" {
		t.Fatalf("expected single warning 'timeout on bing', got %#v", resp.Warnings)
	}

	got := readDegradedCounter(t, m, "web", "unresponsive_engines")
	if got != 1 {
		t.Fatalf("expected ia_buscar_search_degraded_total{web,unresponsive_engines}=1, got %v", got)
	}
}

// TestRecordDegradedPreservesExistingWarnings checks that calling the
// helper twice appends instead of overwrites.
func TestRecordDegradedPreservesExistingWarnings(t *testing.T) {
	m := observability.New()
	observability.SetDefault(m)

	resp := &types.SearchResponse{Warnings: []string{"first"}}
	recordDegraded("dockerhub", "rate_limited", resp, errSample("second"))

	if len(resp.Warnings) != 2 {
		t.Fatalf("expected 2 warnings after second recordDegraded, got %#v", resp.Warnings)
	}
	if resp.Warnings[0] != "first" || resp.Warnings[1] != "second" {
		t.Fatalf("warnings lost or reordered: %#v", resp.Warnings)
	}
}

// TestImagesDegradedPathHitsRecordDegraded wires the helper into the
// images connector: an httptest response with empty results and one
// unresponsive engine must surface a degraded SearchResponse with
// Partial=true and the warning set.
func TestImagesDegradedPathHitsRecordDegraded(t *testing.T) {
	m := observability.New()
	observability.SetDefault(m)

	srv := makeSearxngServer(t, searxngImagesDegraded())
	c := NewImagesConnector(srv.URL, cache.NewService(60))

	resp, err := c.Search(context.Background(), &types.SearchRequest{Query: "x"})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if !resp.Partial {
		t.Fatalf("expected resp.Partial=true on degraded images search")
	}
	if len(resp.Warnings) == 0 {
		t.Fatalf("expected at least one warning on degraded images search")
	}
	got := readDegradedCounter(t, m, "images", "unresponsive_engines")
	if got != 1 {
		t.Fatalf("expected degraded counter=1 for images, got %v", got)
	}
}

// ---- helpers -------------------------------------------------------------

type stringErr struct{ s string }

func (e *stringErr) Error() string { return e.s }

func errSample(s string) error { return &stringErr{s: s} }

func readDegradedCounter(t *testing.T, m *observability.Metrics, source, kind string) float64 {
	t.Helper()
	mfs, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != "ia_buscar_search_degraded_total" {
			continue
		}
		for _, mv := range mf.Metric {
			if labelsMatch(mv.Label, map[string]string{"source": source, "kind": kind}) {
				return mv.Counter.GetValue()
			}
		}
	}
	return 0
}

func labelsMatch(got []*dto.LabelPair, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, lp := range got {
		if want[lp.GetName()] != lp.GetValue() {
			return false
		}
	}
	return true
}

// silenceUnused keeps the strings import in case future tests need it.
var _ = strings.Contains
