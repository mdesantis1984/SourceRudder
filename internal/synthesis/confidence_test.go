package synthesis

import (
	"context"
	"fmt"
	"testing"

	"github.com/mdesantis1984/SourceRudder/pkg/types"
)

func resultsOnHosts(count, hosts int) []types.SearchResultItem {
	out := make([]types.SearchResultItem, count)
	for i := range out {
		out[i] = types.SearchResultItem{
			Title:   fmt.Sprintf("r%d", i),
			URL:     fmt.Sprintf("https://host%d.example.com/p/%d", i%hosts, i),
			Snippet: "golang context cancellation details",
		}
	}
	return out
}

func TestCalculateConfidenceCapsByDistinctHosts(t *testing.T) {
	cases := []struct {
		count, hosts int
		want         float64
	}{
		{0, 0, 0.0},
		{1, 1, 0.3},
		{10, 1, 0.3},
		{10, 2, 0.5},
		{10, 3, 0.75},
		{10, 4, 0.75},
		{10, 5, 0.9},
		{10, 8, 0.9},
		// the count score still caps when hosts are plentiful.
		{1, 5, 0.3},
		{2, 5, 0.5},
		{4, 5, 0.75},
		{3, 3, 0.5},
	}
	for _, tc := range cases {
		items := resultsOnHosts(tc.count, max(tc.hosts, 1))
		if got := calculateConfidence(tc.count, tc.hosts, items); got != tc.want {
			t.Errorf("calculateConfidence(count=%d, hosts=%d) = %v, want %v", tc.count, tc.hosts, got, tc.want)
		}
	}
}

func TestSynthesisConfidenceReflectsSourceDiversity(t *testing.T) {
	svc := NewService()
	ctx := context.Background()
	tainted := resultsOnHosts(10, 1)
	diverse := resultsOnHosts(10, 6)

	sum, err := svc.Summarize(ctx, "golang context", tainted)
	if err != nil || sum.Confidence != 0.3 {
		t.Errorf("Summarize on one host: confidence=%v err=%v, want 0.3", sum.Confidence, err)
	}
	deep, err := svc.DeepResearch(ctx, "golang context", tainted)
	if err != nil || deep.Confidence != 0.3 {
		t.Errorf("DeepResearch on one host: confidence=%v err=%v, want 0.3", deep.Confidence, err)
	}
	deep, err = svc.DeepResearch(ctx, "golang context", diverse)
	if err != nil || deep.Confidence != 0.9 {
		t.Errorf("DeepResearch on six hosts: confidence=%v err=%v, want 0.9", deep.Confidence, err)
	}
	sum, err = svc.Summarize(ctx, "golang context", diverse)
	if err != nil || sum.Confidence != 0.9 {
		t.Errorf("Summarize on six hosts: confidence=%v err=%v, want 0.9", sum.Confidence, err)
	}
}
